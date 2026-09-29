// V5 §4.15 — degraded-passive with every subsystem present: the SP-14 command surface and the
// SP-21 admission switches report the true state of a degraded, disabled or failing seam, and
// never an absence or a success they did not observe.
//
// CURRENT CRITERION (plans/V5-VERIFY §4, row 4.15): "SP-14/SP-21 degraded/error/privacy behavior,
// no absence or misleading success on failure."
//
// Wired: the REAL spawned daemon — internal/cli's runDaemon, started by the real binary's own
// session-start / lazy-spawn path, so the observer, store, DAG, checkpoint writer, rehydrator,
// scheduler and the installed MCP tool set are all the shipped composition — and the real
// `qompack status`, `qompack recall`, `qompack dropped` and `qompack config print` subcommands,
// spawned as processes against it. Nothing in this row is composed in-process: every assertion is
// on a genuine exit code, a genuine stdout envelope, or a genuine file the process left behind.
//
// The historical 4.15 text (V4-era) enumerated recording counts, the idle controller's `ran` list
// and MCP handlers called in-process. Those seams are V4 §4.12's (test/e2e/v4_x12_test.go) and
// V3 §4.8's (v3_x08_test.go), both still live; this row does not re-assert them. It asserts what
// the CURRENT criterion names and those rows do not: the SP-14 surfaces a user actually reads
// while degraded, the honest failure shape of those surfaces when the daemon is unreachable, and
// the SP-21 switch and capture-admission paths when configuration is refused or corrupt.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/commands"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
)

// x15v5Session is this row's session identity.
const x15v5Session = core.SessionID("sess-e2e-v5-x15")

// x15v5ForcedID is the contract assertion the degraded arm forces to a critical failure. It is
// what the status banner must name: a degraded session that cannot say WHY is the "absence" the
// criterion forbids.
const x15v5ForcedID = contract.ID("v5.x15.forced")

// x15v5Marker appears exactly once in the seeded capture, so a recall hit is evidence the store
// indexed the content while degraded rather than evidence the query matched noise.
const x15v5Marker = "x15 degraded passive retrieval marker"

// x15v5ToolUseID and x15v5Path identify the marker capture.
const (
	x15v5ToolUseID = "toolu_v5_x15_marker"
	x15v5Path      = "src/x15_marker.go"
)

// x15v5Turns is how many ordinary PostToolUse events the degraded arm records besides the marker
// capture and the secret fixtures: enough to prove recording continues at all, and every one is a
// real process spawn.
const x15v5Turns = 3

// x15v5NewResultKey is SP-21's feature switch, spelled as config.MigrationGates spells it.
const x15v5NewResultKey = "runtime.migration.replacement.newResult"

// x15v5ConfigViolationsFile is the file internal/cli's persistViolations writes under state/ — the
// on-disk record that a configured value was refused, which is what "recorded as disabled, never as
// passed" is checked against.
const x15v5ConfigViolationsFile = "config-violations.json"

// The two project configurations the negative-control arms are built from.
const (
	// x15v5DaemonOffConfig disables the resident daemon outright. ipc.Client.Send's step 2 then
	// spools without dialling and without lazy-spawning, so the command surface is exercised with
	// NO daemon and no chance of one appearing mid-assertion.
	x15v5DaemonOffConfig = `{"runtime":{"daemon":{"enabled":false}}}`
	// x15v5NewResultOnConfig asks for SP-21's replacement switch, whose gate has not passed.
	x15v5NewResultOnConfig = `{"runtime":{"migration":{"replacement":{"newResult":true}}}}`
)

// x15v5RedactedPlaceholder is the in-place marker redact leaves where a secret was; its presence
// proves the content was stored REDACTED rather than never stored at all.
const x15v5RedactedPlaceholder = "«redacted:"

// x15v5Degrade forces ModeDegradedPassive through the same seam the daemon reads at New: a monitor
// over the project's own state/contract.json (v3_x08, checkpoint_degraded, v4_x12 use the same
// door, and there is no other — the daemon's monitor is an unexported field).
func x15v5Degrade(t *testing.T, p *testutil.Project) {
	t.Helper()
	statePath := filepath.Join(paths.Of(p.Root).State, "contract.json")
	mon := contract.NewMonitor(p.Log, nil, statePath)
	mon.Degrade("v5 x15 degraded-passive row", []contract.Result{{
		ID: x15v5ForcedID, OK: false, Severity: contract.SevCritical,
		Expected: "host contract holds", Observed: "forced by the V5 §4.15 row",
		TS: core.NowMilli(p.Clock),
	}})
	require.Equal(t, contract.ModeDegradedPassive, mon.Mode())
}

// x15v5Hook runs one hook subcommand through the real binary and asserts the two properties every
// hook owes on every path (exit 0, one hookio.Output) plus the degraded-passive clause: a nil
// hookSpecificOutput — no additionalContext, no customInstructions.
func x15v5Hook(t *testing.T, bin string, p *testutil.Project, argv []string, payload []byte) {
	t.Helper()
	stdout, stderr, code := Run(t, bin, argv, payload, e2eEnv(p))
	require.Equal(t, 0, code, "argv=%v must exit 0 even degraded (§12.1)\nstdout:\n%s\nstderr:\n%s",
		argv, stdout, stderr)
	var out hookio.Output
	require.NoError(t, json.Unmarshal(stdout, &out), "argv=%v stdout must be one hookio.Output:\n%s", argv, stdout)
	require.Nil(t, out.HookSpecificOutput,
		"argv=%v: in ModeDegradedPassive every hookSpecificOutput must be absent — nothing may act; got:\n%s",
		argv, stdout)
}

// x15v5Command runs one non-hook subcommand through the real binary against p.
func x15v5Command(t *testing.T, bin string, p *testutil.Project, argv ...string) (stdout, stderr []byte, code int) {
	t.Helper()
	return Run(t, bin, argv, nil, e2eEnv(p))
}

// x15v5Envelope decodes one --json command envelope through the package's own reader, so a schema
// this build cannot read is ErrUnsupported here exactly as it would be for a scripted caller.
func x15v5Envelope(t *testing.T, stdout []byte) commands.Envelope {
	t.Helper()
	env, err := commands.DecodeEnvelope(stdout)
	require.NoError(t, err, "stdout must be one command envelope:\n%s", stdout)
	require.Equal(t, commands.EnvelopeSchema, env.Schema)
	return env
}

// x15v5Status runs `qompack status --json` and returns the decoded status.full report.
//
// status ALWAYS exits 0 and answers ok:true (internal/commands/cmd_status.go): "nothing could be
// reached" is one of its answers, not a failure to produce one. The honesty is in the report's
// provenance, which every caller of this helper goes on to inspect.
func x15v5Status(t *testing.T, bin string, p *testutil.Project) commands.StatusReport {
	t.Helper()
	stdout, stderr, code := x15v5Command(t, bin, p, "status", "--json")
	require.Equal(t, commands.ExitOK, code, "status always answers\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	env := x15v5Envelope(t, stdout)
	require.Equal(t, "status", env.Command)
	require.True(t, env.OK, "status reports unavailability inside the report, never as a failed envelope: %+v", env.Error)
	require.Nil(t, env.Error)

	var rep commands.StatusReport
	require.NoError(t, json.Unmarshal(env.Data, &rep), "status data must be a StatusReport:\n%s", env.Data)
	require.Equal(t, commands.StatusSchema, rep.Schema)
	x15v5AssertQualifiedRows(t, rep)
	return rep
}

// x15v5AssertQualifiedRows is the SP-14 "missing telemetry is unknown, not zero" rule applied to
// every per-hook and per-budget row of one report: a row either carries a measured latency with
// samples behind it and an available provenance, or it carries NO latency, an unavailable/error
// provenance and a reason. A row with numbers and no samples, or with numbers under an unavailable
// provenance, is the misleading success this criterion names.
func x15v5AssertQualifiedRows(t *testing.T, rep commands.StatusReport) {
	t.Helper()
	require.NotEmpty(t, rep.Hooks, "status must render one row per installed hook entry point")
	require.NotEmpty(t, rep.Budgets, "status must render one row per §2.4 budget")

	check := func(kind, name string, lat *commands.Latency, prov commands.Provenance) {
		if lat != nil {
			require.Equal(t, commands.AvailabilityOK, prov.Status,
				"%s %s carries a latency figure, so its provenance must say available: %+v", kind, name, prov)
			require.Positive(t, lat.N,
				"%s %s carries a latency figure with no samples behind it — a number nobody measured", kind, name)
			return
		}
		require.NotEqual(t, commands.AvailabilityOK, prov.Status,
			"%s %s has no latency but claims to be available", kind, name)
		require.NotEmpty(t, prov.Reason,
			"%s %s is unavailable and says nothing about why — an absence, not an observation", kind, name)
	}
	for _, h := range rep.Hooks {
		check("hook", h.Event, h.Latency, h.Provenance)
	}
	for _, b := range rep.Budgets {
		check("budget", string(b.ID), b.Latency, b.Provenance)
	}
}

// x15v5Tool runs one retrieval frontend (`recall`, `dropped`) with --json and returns the exit
// code, the envelope and the ToolResult its Data member carries (zero when there is none).
func x15v5Tool(t *testing.T, bin string, p *testutil.Project, argv ...string) (int, commands.Envelope, commands.ToolResult) {
	t.Helper()
	stdout, stderr, code := x15v5Command(t, bin, p, append(argv, "--json")...)
	require.NotEmpty(t, stdout, "argv=%v: --json must always write an envelope, even on failure\nstderr:\n%s", argv, stderr)
	env := x15v5Envelope(t, stdout)
	require.Equal(t, argv[0], env.Command)
	var res commands.ToolResult
	if len(env.Data) > 0 {
		require.NoError(t, json.Unmarshal(env.Data, &res), "argv=%v data must be a ToolResult:\n%s", argv, env.Data)
	}
	return code, env, res
}

// x15v5ToolBody decodes the single text block a tool answer carries into v.
func x15v5ToolBody(t *testing.T, res commands.ToolResult, v any) {
	t.Helper()
	require.Len(t, res.Content, 1, "a tool result carries exactly one text block: %+v", res.Content)
	require.NoError(t, json.Unmarshal([]byte(res.Content[0].Text), v),
		"the %s body must be JSON: %s", res.Tool, res.Content[0].Text)
}

// x15v5AdminDrain asks the daemon at root to drain its spool now, so a spooled event does not wait
// on the daemon's own idle-tick backstop. Failures are ignored: the caller is polling the index.
func x15v5AdminDrain(root string) {
	addr, err := ipc.Resolve(root)
	if err != nil {
		return
	}
	c := ipc.NewClientWithOptions(addr, nil, nil, nil, ipc.ClientOptions{
		ProjectRoot: root, ConnectDeadline: e2eRoundTripDeadline, AckDeadline: e2eRoundTripDeadline,
	})
	defer func() { _ = c.Close() }()
	_, _ = c.Send(context.Background(), ipc.Request{
		Op: ipc.OpAdminDrain, TS: core.NowMilli(core.SystemClock()), Reply: true,
	}, e2eRoundTripDeadline)
}

// x15v5WaitIndexed waits until index/tool_use.jsonl holds at least want records, driving the
// daemon's drain on each poll rather than waiting on its 30 s backstop.
func x15v5WaitIndexed(t *testing.T, root string, want int) {
	t.Helper()

	// ONE drain, unconditionally, before the index is read for the first time. The condition below
	// returns EARLY when the records are already there, so a wait whose records the async ingest had
	// already published asks for no drain at all while a slower one asks for several — and a drain is
	// not side-effect-free even when it consumes nothing: drainer.Drain calls saveState
	// unconditionally (internal/daemon/drain.go), which always goes through paths.WriteAtomic, so
	// every admin.drain rewrites state/drain.json and mints a transient tmp/wa-* staging file.
	// Without this drive, whether those artifacts exist is decided purely by timing.
	// v4Rig.WaitIndexed carries the same drive for the same reason, and §4.13's write-set comparison
	// is where that coin flip was actually caught. Do not "simplify" it back into the condition.
	x15v5AdminDrain(root)

	require.Eventually(t, func() bool {
		if len(obsToolUseLines(root)) >= want {
			return true
		}
		x15v5AdminDrain(root)
		return len(obsToolUseLines(root)) >= want
	}, obsProcessBound, obsProcessTick,
		"index/tool_use.jsonl never reached %d lines: %s", want, obsWaitDiag{root})
}

// x15v5LiveSessions decodes the status snapshot's session rows and returns the live ones.
func x15v5LiveSessions(t *testing.T, rep commands.StatusReport) []daemon.SessionState {
	t.Helper()
	require.NotNil(t, rep.Snapshot)
	var live []daemon.SessionState
	for _, raw := range rep.Snapshot.Sessions {
		var s daemon.SessionState
		require.NoError(t, json.Unmarshal(raw, &s))
		if s.Live {
			live = append(live, s)
		}
	}
	return live
}

// x15v5ObjectsContaining walks every object on disk, decompressing each, and returns how many
// contain literal. Walking the bytes — rather than trusting a Redacted count — is the point: the
// claim is about what reached objects/.
func x15v5ObjectsContaining(t *testing.T, root, literal string) (hits, objects int) {
	t.Helper()
	err := filepath.WalkDir(paths.Long(paths.Of(root).Objects), func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		plain := raw
		if strings.HasSuffix(path, ".zst") {
			if plain, readErr = store.Decode(raw); readErr != nil {
				return fmt.Errorf("decoding object %s: %w", path, readErr)
			}
		}
		objects++
		if strings.Contains(string(plain), literal) {
			hits++
		}
		return nil
	})
	require.NoError(t, err)
	return hits, objects
}

// x15v5Violations reads state/config-violations.json, or returns nil when it does not exist.
func x15v5Violations(t *testing.T, root string) []config.Violation {
	t.Helper()
	b, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).State, x15v5ConfigViolationsFile)))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var out []config.Violation
	require.NoError(t, json.Unmarshal(b, &out), "state/%s must be a JSON list:\n%s", x15v5ConfigViolationsFile, b)
	return out
}

// x15v5ConfigPrint runs `qompack config print --json` and returns the effective configuration.
func x15v5ConfigPrint(t *testing.T, bin string, p *testutil.Project) config.Config {
	t.Helper()
	stdout, stderr, code := x15v5Command(t, bin, p, "config", "print", "--json")
	require.Equal(t, 0, code, "stderr:\n%s", stderr)
	var got config.Config
	require.NoError(t, json.Unmarshal(stdout, &got), "stdout:\n%s", stdout)
	return got
}

// x15v5CountLines returns how many of lines contain substr.
func x15v5CountLines(lines []string, substr string) int {
	n := 0
	for _, l := range lines {
		if strings.Contains(l, substr) {
			n++
		}
	}
	return n
}

// TestV5_DegradedPassiveIsStillCorrectWithEverySubsystemPresent is V5-VERIFY §4.15.
//
// Five arms, each against the real binary:
//
//   - full_reference: the same commands against an un-degraded daemon, so the degraded arm's mode
//     and banner assertions are known to track the real monitor rather than a fixed string.
//   - degraded: ModeDegradedPassive forced before the daemon exists; status leads with the mode
//     and the NAMED failing assertion; every hook exits 0 with nothing injected; recording and
//     capture-time redaction continue; recall and dropped stay available and answer honestly;
//     two clean SessionStarts restore ModeFull as loudly as the degradation was logged.
//   - daemon_disabled (NEGATIVE CONTROL for the SP-14 surfaces): runtime.daemon.enabled=false
//     severs the producer through a real config key. status must report source none /
//     unavailable with a reason and NO snapshot; recall and dropped must exit 1 with a failed
//     envelope, never ok:true with an empty result.
//   - admission_switch_refused (SP-21): runtime.migration.replacement.newResult=true is refused —
//     the effective value is false and state/config-violations.json names the key with what was
//     asked and what was applied. The default build writes no such record (the control).
//   - config_corrupt_refuses_capture (SP-21 capture admission): the config-corrupt fault site
//     makes the privacy policy unloadable; the hook still exits 0, emits nothing, records the
//     refusal in the quiet log, and leaves no spool, no index and no spawned daemon behind.
func TestV5_DegradedPassiveIsStillCorrectWithEverySubsystemPresent(t *testing.T) {
	bin := Build(t)

	t.Run("full_reference", func(t *testing.T) {
		p := testutil.NewProject(t)
		t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })

		obsRunHook(t, bin, []string{"session-start"}, sessionStartFor(t, p.Root, x15v5Session), e2eEnv(p))
		e2eWaitDaemonUp(t, p.Root)

		rep := x15v5Status(t, bin, p)
		require.Equal(t, commands.SourceDaemon, rep.Primary.Source)
		require.Equal(t, commands.AvailabilityOK, rep.Primary.Status)
		require.NotNil(t, rep.Snapshot)
		require.Equal(t, contract.ModeFull.String(), rep.Snapshot.Mode,
			"a fresh daemon over a clean state file is in ModeFull")
		for _, r := range rep.Snapshot.Contract {
			require.False(t, !r.OK && r.Severity == contract.SevCritical,
				"the reference arm must not itself be degraded by a failing critical assertion: %+v", r)
		}

		human, stderr, code := x15v5Command(t, bin, p, "status")
		require.Equal(t, commands.ExitOK, code, "stderr:\n%s", stderr)
		require.Contains(t, string(human), "mode:        "+contract.ModeFull.String())
		// The banner lists EVERY non-OK result, an info or warn one included, and that is correct:
		// what distinguishes a full-mode session is that none of them carries the critical spelling.
		require.NotContains(t, string(human), "critical — degrades the session when observed",
			"a full-mode session must not render any failure as one that degrades it")
	})

	t.Run("degraded", func(t *testing.T) {
		p := testutil.NewProject(t)
		t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })

		x15v5Degrade(t, p)
		require.Equal(t, 1, x15v5CountLines(loudLines(t, p.Root), "degrading to passive"),
			"exactly one degradation line must have been Loud-logged")

		// ── The daemon comes up on the LAZY-SPAWN path, not on a SessionStart ────────────────────
		// The count of SessionStarts in this arm is load-bearing: every one runs the monitor's
		// RunAll, and TWO consecutive clean cycles restore ModeFull. The daemon is therefore
		// brought up by the first PostToolUse (the marker capture) so that status can be read
		// BEFORE any RunAll has replaced the persisted forced result with a fresh one.
		x15v5Hook(t, bin, p, []string{"observe", "tool"},
			obsToolPayload(t, p.Root, x15v5Session, x15v5ToolUseID, x15v5Path,
				"// "+x15v5Path+"\n// "+x15v5Marker+"\npackage x15\n"))
		e2eWaitDaemonUp(t, p.Root)

		// ── SP-14 status leads with the degraded state and NAMES the failing assertion ───────────
		rep := x15v5Status(t, bin, p)
		require.Equal(t, commands.SourceDaemon, rep.Primary.Source)
		require.NotNil(t, rep.Snapshot)
		require.Equal(t, contract.ModeDegradedPassive.String(), rep.Snapshot.Mode,
			"the daemon must adopt the persisted degradation at New (§12.1: it survives into the next session)")
		var forced *contract.Result
		for i := range rep.Snapshot.Contract {
			if rep.Snapshot.Contract[i].ID == x15v5ForcedID {
				forced = &rep.Snapshot.Contract[i]
			}
		}
		require.NotNil(t, forced, "the persisted failing assertion must be reported, not summarized away: %+v",
			rep.Snapshot.Contract)
		require.False(t, forced.OK)
		require.Equal(t, contract.SevCritical, forced.Severity)

		human, stderr, code := x15v5Command(t, bin, p, "status")
		require.Equal(t, commands.ExitOK, code, "stderr:\n%s", stderr)
		text := string(human)
		require.Contains(t, text, "mode:        "+contract.ModeDegradedPassive.String())
		require.Contains(t, text, "FAILING", "the banner must lead with the failure, not a count of assertions")
		require.Contains(t, text, string(x15v5ForcedID), "the banner must NAME the failing assertion")
		require.Contains(t, text, "critical — degrades the session when observed",
			"a critical failure must not render like a warning")
		require.Contains(t, text, "expected: host contract holds")
		require.Contains(t, text, "observed: forced by the V5 §4.15 row")

		// ── EXACTLY ONE SessionStart, and it is the compact one: no injection while degraded ─────
		compact, err := json.Marshal(hookio.Event{
			HookEventName: "SessionStart", SessionID: x15v5Session, CWD: p.Root, Source: "compact",
		})
		require.NoError(t, err)
		x15v5Hook(t, bin, p, []string{"session-start"}, compact)
		rep = x15v5Status(t, bin, p)
		require.Equal(t, contract.ModeDegradedPassive.String(), rep.Snapshot.Mode,
			"one clean RunAll must not restore yet")
		live := x15v5LiveSessions(t, rep)
		require.Len(t, live, 1, "§12.1 keeps L0/L1 recording: the session must still register while degraded")
		require.Equal(t, x15v5Session, live[0].ID)

		// ── Recording continues: prompts, tool results, and every secret family ──────────────────
		x15v5Hook(t, bin, p, []string{"observe", "prompt"},
			obsPromptPayload(t, p.Root, x15v5Session, "keep recording while the contract is degraded"))
		for i := range x15v5Turns {
			x15v5Hook(t, bin, p, []string{"observe", "tool"},
				obsToolPayload(t, p.Root, x15v5Session, fmt.Sprintf("toolu_v5_x15_%02d", i),
					fmt.Sprintf("src/x15_%02d.go", i), fmt.Sprintf("package x15_%02d\n", i)))
		}
		literals := e2eSecretLiterals()
		for _, sl := range literals {
			payload := readSecretFixture(t, sl.source)
			require.Contains(t, string(payload), sl.literal,
				"fixture sanity: %s must actually contain the literal this arm looks for", sl.source)
			x15v5Hook(t, bin, p, []string{"observe", "tool"},
				obsToolPayload(t, p.Root, x15v5Session, "toolu_v5_x15_"+sl.family,
					"logs/"+sl.family+".log", string(payload)))
		}
		// One record per driven event: the marker capture, the verbatim prompt (indexed under a
		// prompt_ id, as v3_x08's 40+3+1 count records), the ordinary turns and the secret families.
		const markerAndPrompt = 2
		wantRecords := markerAndPrompt + x15v5Turns + len(literals)
		x15v5WaitIndexed(t, p.Root, wantRecords)
		require.Len(t, obsToolUseLines(p.Root), wantRecords,
			"index/tool_use.jsonl must hold exactly one record per driven event")

		// ── SP-21 privacy: capture-time redaction is not an acting path and keeps running ────────
		for _, sl := range literals {
			hits, objects := x15v5ObjectsContaining(t, p.Root, sl.literal)
			require.Positive(t, objects, "the walk must have found objects to inspect")
			require.Zero(t, hits,
				"§13 invariant 7 VIOLATED while degraded: the %s secret reached objects/", sl.family)
		}
		placeholders, _ := x15v5ObjectsContaining(t, p.Root, x15v5RedactedPlaceholder)
		require.Positive(t, placeholders,
			"no object carries a redaction placeholder — the secrets may have been dropped rather "+
				"than redacted in place, which would prove nothing about privacy admission")

		// ── Acting is off: the PreCompact hook seals nothing and emits nothing ───────────────────
		out := cpRunCheckpointHook(t, bin, p, x15v5Session)
		require.Nil(t, out.HookSpecificOutput,
			"handleCheckpoint leaves hookio.Empty() when !mode.MayAct(): no customInstructions")
		require.Empty(t, cpCheckpointArtifacts(t, p.Root), "nothing may be sealed while degraded")
		manifest, err := paths.ReadManifest(paths.Of(p.Root))
		require.NoError(t, err)
		require.Empty(t, manifest, "a manifest line without an artifact would be the worse half of the same failure")

		// ── SP-14 retrieval stays available while degraded (pull-based, cannot mislead) ──────────
		code, envl, res := x15v5Tool(t, bin, p, "recall", x15v5Marker)
		require.Equal(t, commands.ExitOK, code, "recall must answer while degraded: %+v", envl.Error)
		require.True(t, envl.OK)
		require.False(t, res.IsError)
		var recalled struct {
			Hits []struct {
				ToolUseID string `json:"tool_use_id"`
				Path      string `json:"path"`
			} `json:"hits"`
			Count int  `json:"count"`
			Found bool `json:"found"`
		}
		x15v5ToolBody(t, res, &recalled)
		require.True(t, recalled.Found, "recall must find the content a real hook stored while degraded")
		require.Equal(t, len(recalled.Hits), recalled.Count)
		var found bool
		for _, h := range recalled.Hits {
			if h.ToolUseID == x15v5ToolUseID {
				found = true
				require.Equal(t, x15v5Path, h.Path)
			}
		}
		require.True(t, found, "the marker capture must be among the hits: %+v", recalled.Hits)

		code, envl, res = x15v5Tool(t, bin, p, "dropped")
		require.Equal(t, commands.ExitOK, code, "dropped must answer while degraded: %+v", envl.Error)
		require.True(t, envl.OK)
		require.False(t, res.IsError)
		var dropped struct {
			Drops     []json.RawMessage `json:"drops"`
			Count     int               `json:"count"`
			Available *bool             `json:"available"`
			Reason    string            `json:"reason"`
		}
		x15v5ToolBody(t, res, &dropped)
		require.NotNil(t, dropped.Drops, "dropped must always carry a drops array, even when empty")
		require.Equal(t, len(dropped.Drops), dropped.Count)
		require.Empty(t, dropped.Drops, "no rehydration ran while degraded, so Qompack dropped nothing")
		if dropped.Available != nil && !*dropped.Available {
			require.NotEmpty(t, dropped.Reason,
				"an unavailable drop report must say why, or it is indistinguishable from \"nothing dropped\"")
		}

		// Still exactly one degradation line, and no restore yet.
		require.Equal(t, 1, x15v5CountLines(loudLines(t, p.Root), "degrading to passive"))
		require.Zero(t, x15v5CountLines(loudLines(t, p.Root), "restoring full"))

		// ── Restore: the SECOND clean SessionStart brings ModeFull back, as loudly ───────────────
		// It must be source=compact: the PreCompact above armed History.AwaitingCompactStart, and
		// the shipped CSessionStartSourceCompact assertion fails — critically, resetting the clean
		// run counter — on any other source, exactly as it would for a host that skipped the
		// compaction it announced. (Measured: a source=startup here keeps the session degraded.)
		// This call is NOT run through x15v5Hook: RunAll restores ModeFull in phase 1 of the same
		// route, so the reply may legitimately act again — which is the restore working, not a
		// gating defect.
		restoreOut, restoreErr, restoreCode := Run(t, bin, []string{"session-start"}, compact, e2eEnv(p))
		require.Equal(t, 0, restoreCode, "stderr:\n%s", restoreErr)
		requireParsesAsOutput(t, restoreOut)
		rep = x15v5Status(t, bin, p)
		require.Equal(t, contract.ModeFull.String(), rep.Snapshot.Mode,
			"the second consecutive clean RunAll must restore ModeFull (§12.1)")
		require.Equal(t, 1, x15v5CountLines(loudLines(t, p.Root), "restoring full"),
			"the restore must be logged exactly as loudly as the degradation")

		p.AssertAppendOnly(t)
	})

	t.Run("daemon_disabled", func(t *testing.T) {
		// NEGATIVE CONTROL for the SP-14 surfaces. The producer is severed through a real config
		// key: with runtime.daemon.enabled=false every client spools without dialling or spawning,
		// so there is no daemon and none can appear. Every surface must say so.
		p := testutil.NewProject(t, testutil.WithConfig(x15v5DaemonOffConfig))
		t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
		require.False(t, p.Cfg.Runtime.Daemon.Enabled, "the project config must have disabled the daemon")

		rep := x15v5Status(t, bin, p)
		require.Equal(t, commands.SourceNone, rep.Primary.Source,
			"no daemon and no persisted metrics: the report must name NO source, not invent one")
		require.NotEqual(t, commands.AvailabilityOK, rep.Primary.Status)
		require.Contains(t, rep.Primary.Reason, "daemon", "the reason must say what could not be reached")
		require.Contains(t, rep.Primary.Reason, "disk")
		require.Nil(t, rep.Snapshot, "no snapshot may be rendered when nothing answered — no zero-filled one")
		for _, h := range rep.Hooks {
			require.Nil(t, h.Latency, "hook %s: no instrument answered, so no figure may be shown", h.Event)
		}
		for _, b := range rep.Budgets {
			require.Nil(t, b.Latency, "budget %s: no instrument answered, so no figure may be shown", b.ID)
		}

		human, stderr, code := x15v5Command(t, bin, p, "status")
		require.Equal(t, commands.ExitOK, code, "stderr:\n%s", stderr)
		require.Contains(t, string(human), "source: "+string(commands.SourceNone))
		require.NotContains(t, string(human), "mode:", "no mode line may be printed for a daemon nobody reached")

		for _, argv := range [][]string{{"recall", x15v5Marker}, {"dropped"}} {
			code, env, res := x15v5Tool(t, bin, p, argv...)
			require.Equal(t, commands.ExitError, code,
				"%v with no daemon must exit %d — an empty result set would be a lie", argv, commands.ExitError)
			require.False(t, env.OK, "%v: the envelope must not claim success", argv)
			require.NotNil(t, env.Error, "%v: the envelope must carry the failure", argv)
			require.NotEmpty(t, env.Error.Kind)
			require.NotEmpty(t, env.Error.Message)
			if len(env.Data) > 0 {
				require.True(t, res.IsError, "%v: a data member on a failed call must itself be marked is_error", argv)
			}
		}

		addr, err := ipc.Resolve(p.Root)
		require.NoError(t, err)
		require.False(t, ipc.Probe(addr, e2eProbeTimeout),
			"no command may have lazily spawned a daemon the operator disabled")
	})

	t.Run("admission_switch_refused", func(t *testing.T) {
		// The control: a default project asks for nothing gated and records no refusal.
		clean := testutil.NewProject(t)
		got := x15v5ConfigPrint(t, bin, clean)
		require.False(t, got.Runtime.Migration.Replacement.NewResult)
		for _, v := range x15v5Violations(t, clean.Root) {
			require.NotEqual(t, x15v5NewResultKey, v.Key, "the default build must not report a refusal it never made")
		}

		// The arm: the switch is asked for, and the gate has not passed in this build.
		p := testutil.NewProject(t, testutil.WithConfig(x15v5NewResultOnConfig))
		violationsPath := paths.Long(filepath.Join(paths.Of(p.Root).State, x15v5ConfigViolationsFile))
		_ = os.Remove(violationsPath) // whatever NewProject's own load left; the COMMAND must write it

		got = x15v5ConfigPrint(t, bin, p)
		require.False(t, got.Runtime.Migration.Replacement.NewResult,
			"SP-21's replacement switch must stay off until its gate passes; a true value must fall back")

		var refusal *config.Violation
		violations := x15v5Violations(t, p.Root)
		for i := range violations {
			if violations[i].Key == x15v5NewResultKey {
				refusal = &violations[i]
			}
		}
		require.NotNil(t, refusal,
			"the refused switch must be RECORDED in state/%s, not silently reset", x15v5ConfigViolationsFile)
		// Got and Want are rendered as strings on the way to disk (config.Violation's members are
		// `any`; the writer stringifies them), so the comparison is on the rendering.
		require.Equal(t, "true", fmt.Sprint(refusal.Got), "the record must say what was asked for")
		require.Equal(t, "false", fmt.Sprint(refusal.Want), "the record must say what was applied instead")
		// The persisted record carries the key, what was asked and what was applied. It does NOT
		// carry Validate's own "gate ... has not passed" wording — ViolationsFromWarnings rebuilds
		// the message from the generic "invalid value, using default" form — so the WHY is only in
		// the gate table below, not on disk. Recorded in the row's disposition as a finding.
		require.NotEmpty(t, refusal.Message)

		var gate *config.MigrationGate
		gates := config.MigrationGates()
		for i := range gates {
			if gates[i].Key == x15v5NewResultKey {
				gate = &gates[i]
			}
		}
		require.NotNil(t, gate, "the switch must be in the gate table")
		require.False(t, gate.Passed,
			"this arm asserts a refusal; a build whose SP-21 gate has passed must retire this row, not weaken it")
		t.Logf("v5 §4.15: %s refused by gate %q (%s)", x15v5NewResultKey, gate.Gate, gate.Owner)
	})

	t.Run("config_corrupt_refuses_capture", func(t *testing.T) {
		// SP-21 capture admission uses strict configuration: with .qompack/config.json corrupt, the
		// privacy policy cannot be compiled, so the hook must admit NOTHING — and must say so where
		// an operator can find it — while still exiting 0 as §2.3 requires of every hook.
		p := testutil.NewProject(t)
		t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
		env := e2eEnv(p)
		env[qompackFaultEnvKey] = "config-corrupt"

		stdout, stderr, code := Run(t, bin, []string{"observe", "tool"},
			obsToolPayload(t, p.Root, x15v5Session, x15v5ToolUseID, x15v5Path, x15v5Marker), env)
		require.Equal(t, 0, code, "a hook exits 0 whatever happens inside it\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		var out hookio.Output
		require.NoError(t, json.Unmarshal(stdout, &out))
		require.Nil(t, out.HookSpecificOutput)

		// The refusal is recorded, not swallowed.
		quiet, err := filepath.Glob(paths.Long(filepath.Join(paths.Of(p.Root).Logs, "hook-quiet-*.jsonl")))
		require.NoError(t, err)
		require.NotEmpty(t, quiet, "a refused admission must leave a quiet-log line naming the error")
		b, err := os.ReadFile(quiet[0])
		require.NoError(t, err)
		var line struct {
			Err string `json:"err"`
		}
		require.NoError(t, json.Unmarshal([]byte(strings.SplitN(strings.TrimSpace(string(b)), "\n", 2)[0]), &line))
		require.NotEmpty(t, line.Err, "the quiet-log line must carry the error")

		// And nothing claimed the capture succeeded: no spool, no index, no daemon spawned.
		spooled, err := ipc.SpoolFiles(paths.Of(p.Root).Spool)
		require.NoError(t, err)
		require.Empty(t, spooled, "a delivery the policy could not classify must not be spooled as if admitted")
		require.Empty(t, obsToolUseLines(p.Root), "nothing may have been indexed")
		addr, err := ipc.Resolve(p.Root)
		require.NoError(t, err)
		require.False(t, ipc.Probe(addr, e2eProbeTimeout),
			"a hook that admitted nothing has nothing to send and must not have spawned a daemon")
	})
}
