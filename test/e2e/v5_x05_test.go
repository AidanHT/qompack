// V5 §4.5 — an elimination's stale, uncertain and error states survive every shipped surface and
// every old-caller adapter that fronts the ledger.
//
// CURRENT CRITERION (plans/V5-VERIFY-commands-selection-grammar-and-refinements.md §4, row 4.5):
// "SP-20/SP-13/SP-14 stale/uncertain/error state survives every surface and old-caller adapter."
//
// The historical text named four §8.3 SOURCES (MCP, slash command, heuristic, user statement) and
// asserted Health().Records == 4 with one record per source. That guarantee is retired on this
// tree: negknow's IngestUserStatement and Observe/Detector.Scan have no production caller (the
// ledger's own doc comment says source #3 is inert when nobody calls Observe), and
// `qompack pin --eliminated` reaches the ledger through SP-13's record_eliminated tool rather than
// through IngestPin. What EXISTS is two write surfaces and four read surfaces, and the criterion is
// about the STATE crossing them, so that is what this row drives:
//
//	writes  record_eliminated over a real `qompack mcp` child (SP-13)
//	        `qompack pin --eliminated --json` through the real binary (SP-14 frontend → the SP-13
//	        tool over the daemon's mcp op — the old-caller adapter for the slash command)
//	reads   already_tried over a real `qompack mcp` child (SP-13)
//	        the rehydrated digest of SessionStart(source=compact) (SP-11, via the checkpoint the
//	        PreCompact hook sealed)
//	        `qompack status --json` (SP-14; the ledger's counters live in the daemon's registry)
//	        negknow.Answer.MCPResult — the four-tuple adapter that predates AlreadyTriedResult —
//	        read over the on-disk log once every daemon is gone
//
// Every process is the real binary; every daemon is a real spawned one; the staleness flip is the
// production path (negknow.Open's bounded refresh at the next open — rebuild_bloom is planned only
// on a cold TTL, which no test can wait for). SP-20 enters as the capture path: the dependency
// versions the flip is measured against are the observer's own file-version history, written by
// real PostToolUse hooks.
//
// The NEGATIVE CONTROL is the last phase: records/eliminations.jsonl replaced by a directory of the
// same name, which is the portable way to make negknow.Open go blind. Every surface must then say
// "unavailable"/error, never "absent" and never a fabricated success — the exact false negative
// §11.3 invariant 8 forbids, and the one a vacuous version of this row would never catch.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/commands"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/testutil"
)

// x5v5Session is this row's session identity.
const x5v5Session = core.SessionID("sess-e2e-v5-x05")

// The MCP-recorded elimination: the plan's own worked example (Qompack.md §8.3), depending on both
// configuration files. x5v5Synonym classifies to the same approach class ("widen-pool-timeout") by
// negknow.ApproachClass's synonym table, which is what makes the second query a real synonym probe
// rather than a repeat.
const (
	x5v5Target   = "src/auth.ts:refreshToken"
	x5v5Approach = "widen pool timeout"
	x5v5Synonym  = "increasing the connection-pool timeouts"
	x5v5Reason   = "pgbouncer 1.18 ignores it in transaction mode"
)

// The slash-command-recorded elimination, depending only on the lockfile — which never changes,
// so this record is the within-run control: it must stay active while the other flips.
const (
	x5v5PinTarget   = "src/db.ts"
	x5v5PinApproach = "disable pooling"
	x5v5PinReason   = "pool is saturated"
)

// The dependency files, by their project-relative paths, and the tool-use ids the hooks that read
// them carry (obsToolUseLines is polled for these).
const (
	x5v5Compose      = "docker-compose.yml"
	x5v5Lock         = "package-lock.json"
	x5v5ReadCompose1 = "toolu_v5_x05_compose_v1"
	x5v5ReadLock1    = "toolu_v5_x05_lock_v1"
	x5v5ReadCompose2 = "toolu_v5_x05_compose_v2"
)

// x5v5DropConfig is the project configuration of the "uncertain" phase: eliminations.staleResponse
// "drop" is the shipped switch under which a stale record is neither an active prohibition nor an
// absence. It is written to .qompack/config.json exactly as testutil.WithConfig writes one.
const x5v5DropConfig = `{"eliminations":{"staleResponse":"drop"}}`

// x5v5ConfigPerm is the permission testutil's own config write uses (a private file), and
// x5v5DirPerm the one negknow.Open's own EnsureLayout gives a directory.
const (
	x5v5ConfigPerm = 0o600
	x5v5DirPerm    = 0o755
)

// x5v5StaleTag is the tag the digest renders on a stale record: "stale: " plus §8.3's
// re-verification sentence. On this tree that sentence exists as TWO independent literals —
// negknow.StaleNote (what already_tried renders) and internal/rehydrate/items.go's staleStatusTag
// (what the digest renders); neither is derived from the other. Composing the tag from the negknow
// copy and asserting it on the digest is what holds the two copies equal: a drift on either side
// fails subtest 2 here.
const x5v5StaleTag = "[stale: " + negknow.StaleNote + "]"

// x5v5ActiveTag is the digest's tag on an active record (internal/rehydrate's activeStatusTag).
const x5v5ActiveTag = "[active]"

// x5v5Line is the digest's rendering of one elimination up to its reason — the prefix
// rehydrate.eliminationLine emits — so an assertion names THIS record and no other.
func x5v5Line(target, approach string) string {
	return "- " + target + " — \"" + approach + "\" — "
}

// x5v5StartPayload builds a SessionStart event for this row's session and source.
func x5v5StartPayload(t *testing.T, root, source string) []byte {
	t.Helper()
	b, err := json.Marshal(hookio.Event{
		HookEventName: "SessionStart", SessionID: x5v5Session, CWD: root, Source: source,
	})
	require.NoError(t, err)
	return b
}

// x5v5Restart brings a daemon down (when one is up) and spawns a fresh one through the real
// session-start hook with the given source, returning the additionalContext that hook carried
// back ("" for a startup, or when the daemon suppressed it).
//
// The source matters to the §12.1 host contract, not just to the digest: after a PreCompact the
// host's next SessionStart IS the compaction, and a daemon that sees source=startup instead
// records a critical session_start.source_compact failure and degrades to passive — no injection
// at all. So the restart that follows this row's PreCompact is driven by the compact hook itself.
func x5v5Restart(t *testing.T, bin string, p *testutil.Project, env map[string]string, source string) string {
	t.Helper()
	e2eShutdownIfReachable(t, p.Root)
	out := scRunStart(t, bin, env, x5v5StartPayload(t, p.Root, source))
	e2eWaitDaemonUp(t, p.Root)
	if out.HookSpecificOutput == nil {
		return ""
	}
	return out.HookSpecificOutput.AdditionalContext
}

// x5v5InjectTag is the §8.5 injection open tag x4InjectedSeq parses. The polling condition below
// only needs to know whether a payload carries it; the seq is parsed afterwards, on the test
// goroutine, by x4InjectedSeq.
const x5v5InjectTag = "<!-- qompack:injected seq="

// x5v5StartAttempt is one session-start hook run, recorded rather than asserted.
type x5v5StartAttempt struct {
	ac     string // the additionalContext carried back; "" when the hook carried none
	code   int
	stdout string
	stderr string
	runErr error // the process could not be run at all
}

// x5v5TryStart runs one session-start hook through the real binary and records what came back.
// It takes no *testing.T on purpose: it is called from an EventuallyWithT condition, which runs
// off the test goroutine where FailNow is not permitted, so the condition records and the caller
// asserts. The process handling mirrors harness.go's Run — the binary's own directory, the
// inherited environment plus env — so a re-driven hook is the same hook.
func x5v5TryStart(bin string, env map[string]string, payload []byte) x5v5StartAttempt {
	cmd := exec.CommandContext(context.Background(), bin, "session-start")
	cmd.Dir = filepath.Dir(bin)
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &outBuf, &errBuf
	err := cmd.Run()
	at := x5v5StartAttempt{stdout: outBuf.String(), stderr: errBuf.String()}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		at.code = exitErr.ExitCode()
	default:
		at.runErr = err
		return at
	}
	var out hookio.Output
	if at.code == 0 && json.Unmarshal([]byte(strings.TrimSpace(at.stdout)), &out) == nil &&
		out.HookSpecificOutput != nil {
		at.ac = out.HookSpecificOutput.AdditionalContext
	}
	return at
}

// x5v5CompactUntilTagged drives SessionStart(source=compact) until one carries the §8.5 injection
// tag, asserts that the tagged seq is want, and returns the injected context. On a daemon that has
// not opened the ledger yet the compaction is ALSO what opens it — negknow.Open's single production
// call site is the first compaction's deps() in internal/daemon/rehydrate_service.go — so every
// phase that goes on to read the ledger takes a tagged compaction as its asserted precondition,
// never as something it hopes already happened.
//
// Why a first attempt can come back empty on a loaded host with the daemon up and healthy:
// session-start's dial budget is internal/cli/hookclient.go's hookConnectDeadlineFloor (250 ms).
// A daemon that has just spawned, or is momentarily busy, is not guaranteed to have re-posted its
// accept inside that budget; the hook then spools the request and returns an EMPTY output, and the
// daemon's drain replays the spooled compaction with nobody to reply to — the ledger opens and the
// digest is rendered into no stdout at all. Re-driving until the digest actually crosses the hook
// boundary is what lets this row assert the state ON the surface rather than the fate of the one
// request that first carried it. Bounds: mcpE2EIndexBound/mcpE2EIndexTick, the harness's own bound
// for a daemon-side effect to become observable; each attempt is itself bounded by the hook's own
// deadlines, and a spooled attempt returns as soon as its 250 ms dial expires.
//
// The condition never calls require: EventuallyWithT runs it off the test goroutine. A §2.3
// violation (a non-zero exit, or a process that could not run) is recorded and ends the retries at
// once — session-start's only permitted outcome is exit 0 with one hookio.Output, and a retry must
// not paper over a hook that broke that.
func x5v5CompactUntilTagged(t *testing.T, bin string, p *testutil.Project, env map[string]string, want core.CheckpointSeq) string {
	t.Helper()
	payload := x5v5StartPayload(t, p.Root, "compact")

	// EventuallyWithT does not wait for an in-flight condition when its bound expires, so the
	// record is read under the same lock the condition writes it under.
	var (
		mu       sync.Mutex
		last     x5v5StartAttempt
		attempts int
	)
	ok := assert.EventuallyWithT(t, func(c *assert.CollectT) {
		at := x5v5TryStart(bin, env, payload)
		mu.Lock()
		attempts++
		n := attempts
		last = at
		mu.Unlock()
		if at.runErr != nil || at.code != 0 {
			return // recorded; the assertions below report it, and no retry hides it
		}
		if !strings.Contains(at.ac, x5v5InjectTag) {
			c.Errorf("attempt %d: the compact SessionStart carried no injected digest "+
				"(a spooled request — see hookConnectDeadlineFloor — or a suppressed rehydration)\nstdout: %s\nstderr: %s",
				n, at.stdout, at.stderr)
		}
	}, mcpE2EIndexBound, mcpE2EIndexTick)
	mu.Lock()
	at, n := last, attempts
	mu.Unlock()

	require.NoError(t, at.runErr, "session-start could not be run")
	require.Equal(t, 0, at.code, "session-start must exit 0 (§2.3)\nstdout:\n%s\nstderr:\n%s", at.stdout, at.stderr)
	require.True(t, ok, "no compact SessionStart carried an injected digest in %d attempt(s) within %s; %s",
		n, mcpE2EIndexBound, x5v5Diag(t, p.Root))
	if n > 1 {
		t.Logf("compact SessionStart carried its digest on attempt %d; the earlier attempt(s) were spooled "+
			"(hookConnectDeadlineFloor) or suppressed", n)
	}
	seq, tagged := x4InjectedSeq(t, at.ac)
	require.True(t, tagged, "the payload must open with the §8.5 injection tag: %s", at.ac)
	require.Equal(t, want, seq, "the injected digest must name checkpoint %d: %s", want, at.ac)
	return at.ac
}

// x5v5ReadFile drives one PostToolUse Read of path through the real binary and waits for the
// observer to index it — the SP-20 capture path that appends the file version staleness is later
// measured against.
func x5v5ReadFile(t *testing.T, bin string, p *testutil.Project, env map[string]string, id, path, content string) {
	t.Helper()
	obsRunHook(t, bin, []string{"observe", "tool"}, obsToolPayload(t, p.Root, x5v5Session, id, path, content), env)
	require.Eventually(t, func() bool {
		for _, line := range obsToolUseLines(p.Root) {
			if strings.Contains(line, id) {
				return true
			}
		}
		return false
	}, mcpE2EIndexBound, mcpE2EIndexTick, "the observer never indexed %s into index/tool_use.jsonl", id)
}

// x5v5AlreadyTried drives already_tried over a real `qompack mcp` child.
func x5v5AlreadyTried(t *testing.T, c *mcpE2EChild, id int, target, approach string) mcp.AlreadyTriedResult {
	t.Helper()
	var got mcp.AlreadyTriedResult
	mcpE2ECall(t, c, id, mcp.ToolAlreadyTried, map[string]any{"target": target, "approach": approach}, &got)
	return got
}

// x5v5Eliminated is record_eliminated's acknowledgement body, as both write surfaces return it.
type x5v5Eliminated struct {
	ID                  string     `json:"id"`
	Scope               string     `json:"scope"`
	Evidence            string     `json:"evidence"`
	DependsOn           []core.Dep `json:"depends_on"`
	DependsOnUnresolved []string   `json:"depends_on_unresolved"`
	Status              string     `json:"status"`
	Warnings            []string   `json:"warnings"`
}

// x5v5Command runs one slash-command subcommand through the real binary under --json and decodes
// its envelope through commands.DecodeEnvelope — the versioned reader SP-14 ships for old callers.
// The exit code is returned, never asserted: it is one of the things a phase checks.
func x5v5Command(t *testing.T, bin string, env map[string]string, args ...string) (commands.Envelope, int) {
	t.Helper()
	stdout, stderr, code := Run(t, bin, append(args, "--json"), nil, env)
	envl, err := commands.DecodeEnvelope(stdout)
	require.NoError(t, err, "%v must write one decodable envelope\nstdout:\n%s\nstderr:\n%s", args, stdout, stderr)
	require.Equal(t, commands.EnvelopeSchema, envl.Schema)
	return envl, code
}

// x5v5PinEliminated runs `qompack pin --eliminated` and returns its envelope and exit code.
func x5v5PinEliminated(t *testing.T, bin string, env map[string]string, target, approach, reason, dependsOn string) (commands.Envelope, int) {
	t.Helper()
	return x5v5Command(t, bin, env, "pin", "--eliminated",
		"--target", target, "--approach", approach, "--reason", reason,
		"--depends-on", dependsOn, "--scope", string(negknow.ScopeProject))
}

// x5v5ToolResult decodes the envelope's Data as the ToolResult a retrieval frontend passes on.
func x5v5ToolResult(t *testing.T, envl commands.Envelope) commands.ToolResult {
	t.Helper()
	var res commands.ToolResult
	require.NoError(t, json.Unmarshal(envl.Data, &res), "pin's data member must be a ToolResult: %s", envl.Data)
	require.Equal(t, mcp.ToolRecordEliminated, res.Tool, "pin --eliminated dispatches SP-13's own write tool")
	require.NotEmpty(t, res.Content, "a tool result carries its content through the envelope")
	return res
}

// x5v5StatusCounters runs `qompack status --json` and returns the live daemon's counters.
func x5v5StatusCounters(t *testing.T, bin string, env map[string]string) map[string]int64 {
	t.Helper()
	envl, code := x5v5Command(t, bin, env, "status")
	require.Equal(t, commands.ExitOK, code, "status always exits 0; it reports rather than fails")
	require.True(t, envl.OK, "status's envelope: %+v", envl.Error)
	var rep commands.StatusReport
	require.NoError(t, json.Unmarshal(envl.Data, &rep))
	require.Equal(t, commands.StatusSchema, rep.Schema)
	require.Equal(t, commands.SourceDaemon, rep.Primary.Source,
		"a live daemon must be the status source, not the persisted fallback: %+v", rep.Primary)
	require.Equal(t, commands.AvailabilityOK, rep.Primary.Status)
	require.NotNil(t, rep.Snapshot, "a daemon-sourced report carries the daemon's own snapshot")
	return rep.Snapshot.Counters
}

// x5v5Diag renders the daemon's mode, its contract report and the project's LOUD lines, for a
// failure message that says WHY a rehydration or a write was suppressed.
func x5v5Diag(t *testing.T, root string) string {
	t.Helper()
	snap := e2eStatus(t, root)
	b, _ := json.Marshal(snap.Contract)
	return "mode=" + snap.Mode + " contract=" + string(b) + " loud=" + strings.Join(loudLines(t, root), " | ")
}

// x5v5RecordLog is the elimination log's path.
func x5v5RecordLog(root string) string {
	return filepath.Join(paths.Of(root).Records, "eliminations.jsonl")
}

// TestV5_EliminationThroughEveryFourSurfaces is V5-VERIFY §4.5.
func TestV5_EliminationThroughEveryFourSurfaces(t *testing.T) {
	bin := Build(t)
	p := testutil.NewProject(t)
	shutdown := sync.OnceFunc(func() { e2eShutdownIfReachable(t, p.Root) })
	t.Cleanup(shutdown)
	env := e2eEnv(p)

	p.WithFiles(t, map[string]string{
		"src/auth.ts": authTSV1,
		x5v5Compose:   composeV1,
		x5v5Lock:      lockJSONV1,
	})

	// What the earlier phases hand the adapter phase: the MCP record's id and evidence, and the
	// already_tried rendering of each of the three states — stale, uncertain, unavailable — that the
	// four-tuple adapter is compared against, state by state.
	var (
		mcpRecord        x5v5Eliminated
		pinRecord        x5v5Eliminated
		staleOnMCP       mcp.AlreadyTriedResult
		uncertainOnMCP   mcp.AlreadyTriedResult
		unavailableOnMCP mcp.AlreadyTriedResult
	)

	// ── Phase A: both write surfaces record ACTIVE, and the checkpoint freezes both ──────────────
	t.Run("active_through_both_write_surfaces", func(t *testing.T) {
		x5v5Restart(t, bin, p, env, "startup")

		// The SP-20 capture path seeds the store's file-version history BEFORE anything depends on
		// it: resolveDeps can only turn a path into a staleness baseline the store knows a version of.
		x5v5ReadFile(t, bin, p, env, x5v5ReadCompose1, x5v5Compose, composeV1)
		x5v5ReadFile(t, bin, p, env, x5v5ReadLock1, x5v5Lock, lockJSONV1)

		// The first compaction opens the ledger (no checkpoint exists yet, so seq 0). It is an
		// ASSERTED precondition: a compaction whose hook output came back empty may still be
		// sitting in the spool, and a record_eliminated issued before the drain replays it reaches
		// a daemon with no ledger at all — which answers a non-error "not present in this build"
		// body, not a record.
		x5v5CompactUntilTagged(t, bin, p, env, core.CheckpointSeq(0))

		// Write surface 1: record_eliminated over the real stdio child.
		c := mcpE2EStart(t, bin, p)
		t13Initialize(t, c, 1)
		ack := mcpE2ECall(t, c, 2, mcp.ToolRecordEliminated, map[string]any{
			"target": x5v5Target, "approach": x5v5Approach, "reason": x5v5Reason,
			"scope": string(negknow.ScopeProject), "depends_on": []string{x5v5Compose, x5v5Lock},
		}, &mcpRecord)
		require.Equal(t, string(negknow.StatusActive), mcpRecord.Status,
			"record_eliminated must acknowledge an active record; body: %s", ack.Content[0].Text)
		require.NotEmpty(t, mcpRecord.ID)
		require.Regexp(t, `^sha256:[0-9a-f]{64}$`, mcpRecord.Evidence, "the reason text is stored as evidence")
		require.Len(t, mcpRecord.DependsOn, 2,
			"both files have a stored version, so both become staleness dependencies: %+v", mcpRecord)
		require.Empty(t, mcpRecord.DependsOnUnresolved)

		// Read surface 1: already_tried, exact phrasing and a synonym.
		exact := x5v5AlreadyTried(t, c, 3, x5v5Target, x5v5Approach)
		require.Equal(t, "active", exact.State, "%+v", exact)
		require.Equal(t, x5v5Reason, exact.Reason, "the exact stored reason travels with the state")
		require.Equal(t, mcpRecord.Evidence, exact.Evidence)
		require.Equal(t, string(negknow.ScopeProject), exact.Scope)
		require.False(t, exact.Degraded)
		syn := x5v5AlreadyTried(t, c, 4, x5v5Target, x5v5Synonym)
		require.Equal(t, "active", syn.State, "a synonym phrasing classifies onto the same record: %+v", syn)
		require.Equal(t, x5v5Reason, syn.Reason)

		// Write surface 2: the SP-14 slash command, through the real binary, over the daemon's mcp
		// op — the old-caller adapter in front of SP-13's tool.
		envl, code := x5v5PinEliminated(t, bin, env, x5v5PinTarget, x5v5PinApproach, x5v5PinReason, x5v5Lock)
		require.Equal(t, commands.ExitOK, code)
		require.True(t, envl.OK, "pin --eliminated must succeed against a live ledger: %+v", envl.Error)
		require.Equal(t, "pin", envl.Command)
		res := x5v5ToolResult(t, envl)
		require.False(t, res.IsError)
		require.NoError(t, json.Unmarshal([]byte(res.Content[0].Text), &pinRecord), "%s", res.Content[0].Text)
		require.Equal(t, string(negknow.StatusActive), pinRecord.Status)
		require.NotEmpty(t, pinRecord.ID)
		require.NotEqual(t, mcpRecord.ID, pinRecord.ID)
		require.Len(t, pinRecord.DependsOn, 1, "%+v", pinRecord)
		require.Empty(t, pinRecord.DependsOnUnresolved)

		// The slash command's write is visible on the MCP read surface: one ledger, two doors.
		viaPin := x5v5AlreadyTried(t, c, 5, x5v5PinTarget, x5v5PinApproach)
		require.Equal(t, "active", viaPin.State, "%+v", viaPin)
		require.Equal(t, x5v5PinReason, viaPin.Reason)
		c.finish(t)

		// The PreCompact hook seals a checkpoint carrying BOTH records with their status frozen at
		// seal time. It is what lets the digest after the flip show the stale record at all: the
		// rehydrator unions Active() with the checkpoint's copy and re-reads each frozen record's
		// CURRENT status through Get.
		// Criterion change (C1.12, C1.18): this used to read a hookSpecificOutput on the hook's stdout
		// as the full-mode signal. The host rejects any PreCompact hookSpecificOutput and the focus
		// instruction it carried is retired, so the host-facing answer is held to the host's
		// PreCompact contract and the seal is proven by the artifact that follows.
		out := cpRunCheckpointHook(t, bin, p, x5v5Session)
		cpRequireHostConformingPreCompact(t, out)
		require.Equal(t, []string{"0001.json"}, cpCheckpointArtifacts(t, p.Root))
		raw, err := os.ReadFile(paths.Long(paths.CheckpointPath(paths.Of(p.Root), core.CheckpointSeq(1))))
		require.NoError(t, err)
		sealed, err := checkpoint.Unmarshal(raw)
		require.NoError(t, err)
		ids := make([]string, 0, len(sealed.Eliminated))
		for _, rec := range sealed.Eliminated {
			ids = append(ids, rec.ID)
			require.Equal(t, negknow.StatusActive, rec.Status, "frozen at seal time, both are active: %+v", rec)
		}
		require.ElementsMatch(t, []string{mcpRecord.ID, pinRecord.ID}, ids,
			"the sealed checkpoint must carry exactly the two eliminations")
	})
	if t.Failed() {
		t.FailNow()
	}

	// ── Phase B: the dependency changes, and STALE survives every read surface ───────────────────
	t.Run("stale_survives_every_read_surface", func(t *testing.T) {
		// A real hook captures the rewritten compose file: a new file version in the store, which
		// is what the ledger's dependency-hash comparison is measured against (SP-20 → negknow).
		x5v5ReadFile(t, bin, p, env, x5v5ReadCompose2, x5v5Compose, composeV2)

		// The production flip: the next open runs its bounded staleness refresh. rebuild_bloom is
		// planned only on a cold TTL, so a restart is the path a real session actually walks — and
		// after a PreCompact the host's next SessionStart is the compaction, so that hook is the
		// one that spawns the daemon, opens the ledger, flips the record and renders the digest.
		//
		// Read surface 2: the rehydrated digest, built from the sealed checkpoint's frozen copy
		// re-read through the freshly opened ledger.
		//
		// The spawning hook's own output can be empty on a loaded host even though the daemon
		// came up fine: session-start's dial budget is internal/cli/hookclient.go's
		// hookConnectDeadlineFloor (250 ms), and a daemon that has JUST spawned may not have its
		// accept re-posted inside it — the hook spools the request, returns an empty output, and
		// the drain replays the compaction with nobody to reply to. That is a spooled request, not
		// a digest defect, so the digest is re-driven until it actually crosses the hook boundary
		// and only THEN held to seq 1 and its content.
		ac := x5v5Restart(t, bin, p, env, "compact")
		if _, tagged := x4InjectedSeq(t, ac); !tagged {
			t.Logf("the spawning compact SessionStart carried no digest (spooled behind hookConnectDeadlineFloor); "+
				"re-driving — %s", x5v5Diag(t, p.Root))
			ac = x5v5CompactUntilTagged(t, bin, p, env, core.CheckpointSeq(1))
		}
		seq, tagged := x4InjectedSeq(t, ac)
		require.True(t, tagged, "the payload must open with the §8.5 injection tag: %s", ac)
		require.Equal(t, core.CheckpointSeq(1), seq)
		require.Contains(t, ac, "## 3. Approaches already eliminated", "%s", ac)
		mcpLine := x5v5Line(x5v5Target, x5v5Approach)
		pinLine := x5v5Line(x5v5PinTarget, x5v5PinApproach)
		require.Contains(t, ac, mcpLine, "the MCP-recorded elimination must be rendered: %s", ac)
		require.Contains(t, ac, pinLine, "the slash-command elimination must be rendered: %s", ac)
		_, mcpAfter, mcpFound := strings.Cut(ac, mcpLine)
		require.True(t, mcpFound, "the MCP-recorded elimination must be rendered: %s", ac)
		mcpRendered := mcpLine + mcpAfter
		if nl := strings.IndexByte(mcpRendered, '\n'); nl >= 0 {
			mcpRendered = mcpRendered[:nl]
		}
		require.Contains(t, mcpRendered, x5v5StaleTag,
			"the record whose dependency changed must carry §8.3's stale note, em dash included: %s", mcpRendered)
		_, pinAfter, pinFound := strings.Cut(ac, pinLine)
		require.True(t, pinFound, "the slash-command elimination must be rendered: %s", ac)
		pinRendered := pinLine + pinAfter
		if nl := strings.IndexByte(pinRendered, '\n'); nl >= 0 {
			pinRendered = pinRendered[:nl]
		}
		require.Contains(t, pinRendered, x5v5ActiveTag,
			"CONTROL: the record whose dependency did not change must still render active: %s", pinRendered)
		require.NotContains(t, pinRendered, x5v5StaleTag)

		// Read surface 1: already_tried over a NEW stdio child against the NEW daemon.
		c := mcpE2EStart(t, bin, p)
		t13Initialize(t, c, 1)
		staleOnMCP = x5v5AlreadyTried(t, c, 2, x5v5Target, x5v5Approach)
		require.Equal(t, "stale", staleOnMCP.State, "%+v", staleOnMCP)
		require.Equal(t, x5v5Reason, staleOnMCP.Reason, "the exact stored reason survives the flip")
		require.Equal(t, negknow.StaleNote, staleOnMCP.Note, "§8.3's note, verbatim")
		require.Contains(t, staleOnMCP.Note, "—", "the note carries the em dash the plan spells")
		require.NotEmpty(t, staleOnMCP.StaleBecause, "stale_because names what changed: %+v", staleOnMCP)
		require.Contains(t, strings.Join(staleOnMCP.StaleBecause, "\n"), x5v5Compose,
			"the changed dependency is the compose file: %+v", staleOnMCP.StaleBecause)
		require.Equal(t, mcpRecord.Evidence, staleOnMCP.Evidence)
		require.Equal(t, string(negknow.ScopeProject), staleOnMCP.Scope)
		require.False(t, staleOnMCP.Degraded, "a backed stale answer is not a degradation")
		syn := x5v5AlreadyTried(t, c, 3, x5v5Target, x5v5Synonym)
		require.Equal(t, "stale", syn.State, "the synonym phrasing sees the same state: %+v", syn)
		control := x5v5AlreadyTried(t, c, 4, x5v5PinTarget, x5v5PinApproach)
		require.Equal(t, "active", control.State,
			"CONTROL: the lockfile never changed, so the slash-command record stays active: %+v", control)
		c.finish(t)

		// Read surface 3: `qompack status --json`. The ledger is opened with the daemon's own
		// registry, so its counters are the status document's counters.
		counters := x5v5StatusCounters(t, bin, env)
		require.Positive(t, counters["negknow.stale.flipped"],
			"the daemon's own refresh flipped the record; status must show it: %v", counters)
		require.Positive(t, counters["negknow.query.stale"],
			"the stale answers above were counted by the daemon's ledger: %v", counters)
	})
	if t.Failed() {
		t.FailNow()
	}

	// ── Phase C: UNCERTAIN under staleResponse "drop", through the same surfaces ─────────────────
	t.Run("uncertain_under_stale_response_drop", func(t *testing.T) {
		e2eShutdownIfReachable(t, p.Root)
		cfgPath := filepath.Join(paths.Of(p.Root).Dot, "config.json")
		require.NoError(t, paths.WriteAtomic(cfgPath, []byte(x5v5DropConfig), x5v5ConfigPerm))
		t.Cleanup(func() { require.NoError(t, os.Remove(paths.Long(cfgPath))) })
		x5v5Restart(t, bin, p, env, "startup")

		// The digest EXCLUDES the stale record under "drop" and keeps the active one. The sealed
		// checkpoint is still 0001.json, so the digest names seq 1.
		ac := x5v5CompactUntilTagged(t, bin, p, env, core.CheckpointSeq(1))
		require.Contains(t, ac, x5v5Line(x5v5PinTarget, x5v5PinApproach), "%s", ac)
		require.NotContains(t, ac, x5v5Line(x5v5Target, x5v5Approach),
			`under staleResponse "drop" the stale record's detail is withheld from the digest: %s`, ac)
		require.NotContains(t, ac, x5v5StaleTag)

		// already_tried: neither a prohibition nor an absence, and no stale detail disclosed.
		c := mcpE2EStart(t, bin, p)
		t13Initialize(t, c, 1)
		got := x5v5AlreadyTried(t, c, 2, x5v5Target, x5v5Approach)
		uncertainOnMCP = got
		require.Equal(t, "uncertain", got.State, "%+v", got)
		require.NotEqual(t, "absent", got.State)
		require.NotEmpty(t, got.Reason, "an uncertain answer says what could not be established")
		require.NotEmpty(t, got.Note, "and what would resolve it")
		require.NotEqual(t, x5v5Reason, got.Reason, "the withheld record's reason must not leak: %+v", got)
		require.Empty(t, got.StaleBecause, "the withheld record's staleness detail must not leak: %+v", got)
		require.False(t, got.Degraded, "uncertain is the ledger ANSWERING, not a missing ledger")
		control := x5v5AlreadyTried(t, c, 3, x5v5PinTarget, x5v5PinApproach)
		require.Equal(t, "active", control.State, "CONTROL: an active record is unaffected by drop: %+v", control)
		c.finish(t)

		counters := x5v5StatusCounters(t, bin, env)
		require.Positive(t, counters["negknow.query.uncertain"],
			"status must carry the uncertain answers the ledger counted: %v", counters)
	})
	if t.Failed() {
		t.FailNow()
	}

	// ── Phase D, the NEGATIVE CONTROL: a blind ledger — UNAVAILABLE/error, never absence ─────────
	t.Run("negative_control_blind_ledger_is_unavailable_not_absent", func(t *testing.T) {
		e2eShutdownIfReachable(t, p.Root)
		logPath := x5v5RecordLog(p.Root)
		aside := logPath + ".aside"
		require.NoError(t, os.Rename(paths.Long(logPath), paths.Long(aside)))
		require.NoError(t, os.MkdirAll(paths.Long(logPath), x5v5DirPerm),
			"a directory where the log should be is what makes negknow.Open go blind")
		t.Cleanup(func() {
			// Restore the log for the adapter phase: it is the state the rest of this row proved.
			e2eShutdownIfReachable(t, p.Root)
			require.NoError(t, os.Remove(paths.Long(logPath)))
			require.NoError(t, os.Rename(paths.Long(aside), paths.Long(logPath)))
		})
		x5v5Restart(t, bin, p, env, "startup")

		// The compaction still opens the ledger; it comes up blind. Driving it to a tagged digest is
		// what guarantees the open happened BEFORE already_tried is asked: a daemon with no ledger
		// at all answers a non-error body with no state, which is neither the "unavailable" this
		// control asserts nor the "absent" it forbids. What the digest does with the checkpoint's
		// frozen copy in that state is recorded, not asserted (see the disposition).
		ac := x5v5CompactUntilTagged(t, bin, p, env, core.CheckpointSeq(1))
		t.Logf("blind-ledger digest carries the MCP record's line: %v; the stale tag: %v",
			strings.Contains(ac, x5v5Line(x5v5Target, x5v5Approach)), strings.Contains(ac, x5v5StaleTag))

		c := mcpE2EStart(t, bin, p)
		t13Initialize(t, c, 1)
		got := x5v5AlreadyTried(t, c, 2, x5v5Target, x5v5Approach)
		unavailableOnMCP = got
		require.Equal(t, "unavailable", got.State,
			"NEGATIVE CONTROL: a ledger that cannot be consulted must say so; \"absent\" here would be "+
				"the false negative that lets a stale-or-worse elimination vanish: %+v", got)
		require.NotEqual(t, "absent", got.State)
		require.NotEqual(t, "stale", got.State, "nothing on record can be confirmed from a blind ledger")
		require.True(t, got.Degraded, "an unavailable answer is marked degraded")
		require.NotEmpty(t, got.Reason)
		require.NotEmpty(t, got.Note)
		require.Empty(t, got.StaleBecause, "a blind ledger discloses nothing: %+v", got)
		control := x5v5AlreadyTried(t, c, 3, x5v5PinTarget, x5v5PinApproach)
		require.Equal(t, "unavailable", control.State, "the active record is just as unreachable: %+v", control)
		c.finish(t)

		// The slash-command adapter must carry the write failure through, never a fabricated ack.
		envl, code := x5v5PinEliminated(t, bin, env, "src/cache.ts", "add caching", "cache never warms", x5v5Lock)
		require.Equal(t, commands.ExitError, code, "a write the ledger refused is a command error: %+v", envl)
		require.False(t, envl.OK)
		require.NotNil(t, envl.Error, "the envelope names the failure")
		require.NotEqual(t, commands.ErrorKindUsage, envl.Error.Kind, "the invocation was well-formed: %+v", envl.Error)
		require.NotContains(t, string(envl.Data), `"status":"active"`,
			"no acknowledgement may claim the elimination was recorded: %s", envl.Data)
		// The frontend keeps a failed tool's content (commands.callTool: "a failed retrieval that
		// says why is more useful than an error message this package invented"), so the data
		// member is REQUIRED here — an empty one would be the adapter dropping the tool's own error.
		require.NotEmpty(t, envl.Data, "a tool error must travel inside the data member: %+v", envl)
		var res commands.ToolResult
		require.NoError(t, json.Unmarshal(envl.Data, &res))
		require.True(t, res.IsError, "the tool's own error travels inside the data member: %+v", res)

		// status reports the degradation the daemon's ledger counted, and still exits 0.
		counters := x5v5StatusCounters(t, bin, env)
		require.Positive(t, counters["negknow.bloom.blind_mode"],
			"status must carry the blind-mode entry the ledger counted: %v", counters)
	})
	if t.Failed() {
		t.FailNow()
	}

	// ── Phase E: the old-caller adapter, over the on-disk log, with every daemon gone ────────────
	t.Run("old_caller_adapter_agrees_with_the_wire", func(t *testing.T) {
		e2eShutdownIfReachable(t, p.Root)

		// Store is deliberately nil: negknow.Open's refreshAtOpen returns before touching anything
		// when Deps.Store is nil, so the stale state asserted below can only come from the op:stale
		// control line subtest 2's daemon appended — a re-flip against the store is impossible here,
		// and that is what makes "durable in the log" a proven claim rather than a plausible one.
		// Query and Health never need the store.
		deps := negknow.Deps{Store: nil, Session: x5v5Session, Log: p.Log, Clock: p.Clock}
		led, err := negknow.Open(p.Root, p.Cfg, nil, deps)
		require.NoError(t, err)
		t.Cleanup(func() { _ = led.Close() })

		health := led.Health()
		require.Equal(t, 2, health.Records, "%+v", health)
		require.Equal(t, 1, health.Stale, "%+v", health)
		require.Equal(t, 1, health.Active, "%+v", health)

		ans, err := led.Query(t.Context(), x5v5Target, x5v5Approach, negknow.ScopeSession)
		require.NoError(t, err)
		require.Equal(t, negknow.AnswerStale, ans.State,
			"the flip is durable in the log, not in a process: with no store there is nothing to re-flip against")
		state, reason, note, evidence := ans.MCPResult()
		require.Equal(t, staleOnMCP.State, state, "the four-tuple adapter and the wire agree on the state")
		require.Equal(t, staleOnMCP.Reason, reason)
		require.Equal(t, staleOnMCP.Note, note)
		require.Equal(t, staleOnMCP.Evidence, evidence)

		all, err := led.All(t.Context())
		require.NoError(t, err)
		require.Len(t, all, 2)
		for _, rec := range all {
			switch rec.ID {
			case mcpRecord.ID:
				require.Equal(t, negknow.SourceMCP, rec.Source)
				require.Equal(t, negknow.StatusStale, rec.Status)
			case pinRecord.ID:
				// The shipped path is deterministic: `qompack pin --eliminated` reaches the ledger
				// through SP-13's record_eliminated handler, which stamps SourceMCP. §8.3 names the
				// slash command as source #2 and negknow.IngestPin would stamp SourceSlashCommand,
				// but IngestPin has no production caller — the disposition routes that divergence
				// to the SP-14 owner. Asserting the shipped stamp exactly means a later change that
				// flips it is seen here, not absorbed.
				require.Equal(t, negknow.SourceMCP, rec.Source,
					"pin --eliminated is stamped by the record_eliminated handler it dispatches to")
				require.Equal(t, negknow.StatusActive, rec.Status)
			default:
				require.Failf(t, "unexpected record", "%+v", rec)
			}
		}
		require.NoError(t, led.Close())

		// ERROR through the adapter: the same severance Phase D used, read in-process. A blind
		// ledger's Query must reach the adapter as AnswerUnavailable, and the four-tuple must be
		// exactly what the wire rendered in Phase D — never the "absent" tuple.
		logPath := x5v5RecordLog(p.Root)
		aside := logPath + ".aside"
		require.NoError(t, os.Rename(paths.Long(logPath), paths.Long(aside)))
		require.NoError(t, os.MkdirAll(paths.Long(logPath), x5v5DirPerm))
		restored := false
		restore := func() {
			if restored {
				return
			}
			restored = true
			require.NoError(t, os.Remove(paths.Long(logPath)))
			require.NoError(t, os.Rename(paths.Long(aside), paths.Long(logPath)))
		}
		t.Cleanup(restore)

		blind, err := negknow.Open(p.Root, p.Cfg, nil, deps)
		require.NoError(t, err, "a blind ledger opens; it does not fail to open")
		ans, err = blind.Query(t.Context(), x5v5Target, x5v5Approach, negknow.ScopeSession)
		require.NoError(t, err)
		require.Equal(t, negknow.AnswerUnavailable, ans.State,
			"a ledger that cannot be consulted answers unavailable through the adapter too, never absent")
		state, reason, note, evidence = ans.MCPResult()
		require.Equal(t, "unavailable", state)
		require.Equal(t, unavailableOnMCP.State, state, "the four-tuple adapter and the wire agree on the error state")
		require.NotEmpty(t, reason)
		require.Equal(t, unavailableOnMCP.Reason, reason)
		require.NotEmpty(t, note)
		require.Equal(t, unavailableOnMCP.Note, note)
		require.Empty(t, evidence, "a blind ledger discloses no evidence")
		require.NoError(t, blind.Close())
		restore()

		// UNCERTAIN through the adapter: the same real switch Phase C used — the resolved config
		// with eliminations.staleResponse = "drop" (x5v5DropConfig) — over the restored log. The
		// four-tuple must be what the wire rendered in Phase C, and must not leak the withheld
		// record's reason.
		dropCfg := p.Cfg
		dropCfg.Eliminations.StaleResponse = "drop"
		dropped, err := negknow.Open(p.Root, dropCfg, nil, deps)
		require.NoError(t, err)
		t.Cleanup(func() { _ = dropped.Close() })
		health = dropped.Health()
		require.Equal(t, 2, health.Records, "the restored log is whole again: %+v", health)
		ans, err = dropped.Query(t.Context(), x5v5Target, x5v5Approach, negknow.ScopeSession)
		require.NoError(t, err)
		require.Equal(t, negknow.AnswerUncertain, ans.State,
			`under staleResponse "drop" a stale match is uncertain through the adapter too, never absent`)
		state, reason, note, evidence = ans.MCPResult()
		require.Equal(t, "uncertain", state)
		require.Equal(t, uncertainOnMCP.State, state, "the four-tuple adapter and the wire agree on the uncertain state")
		require.NotEmpty(t, reason)
		require.NotEqual(t, x5v5Reason, reason, "the withheld record's reason must not leak through the adapter")
		require.Equal(t, uncertainOnMCP.Reason, reason)
		require.NotEmpty(t, note)
		require.Equal(t, uncertainOnMCP.Note, note)
		require.Empty(t, evidence, "the withheld record's evidence must not leak through the adapter")
	})
}
