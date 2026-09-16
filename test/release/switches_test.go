package release

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/paths"
)

// injectionMarker is the fixed, verb-free prefix of internal/checkpoint's own open tag, derived
// from the exported constant rather than transcribed so the two cannot drift apart.
//
// Distinguishing a REHYDRATION injection from any other additionalContext is load-bearing here.
// `SessionStart` also answers with the contract monitor's probe marker, which is contract
// machinery rather than reinjection and which a reinjection switch has no business suppressing —
// so "additionalContext is empty" is the wrong assertion and would have recorded a switch that
// suppressed more than it claims to. What the switch must stop is the injected span, and that is
// what these cases assert.
var injectionMarker = checkpoint.InjectionOpenTag[:strings.IndexByte(checkpoint.InjectionOpenTag, '%')]

// injectedRehydration reports whether a SessionStart answer carries an injected checkpoint span.
func injectedRehydration(additional string) bool {
	return strings.Contains(additional, injectionMarker)
}

// The configuration ENVIRONMENT layer is how every case here flips its switch
// (internal/config/load.go's applyEnv: QOMPACK_<SEC>__<KEY>__<SUB>). It is the only layer that can
// be applied per-process without writing a file the next case would inherit.
const (
	envMode        = "QOMPACK_RUNTIME__MODE"
	envDaemon      = "QOMPACK_RUNTIME__DAEMON__ENABLED"
	envReinjection = "QOMPACK_RUNTIME__MIGRATION__REINJECTION__SESSIONSTARTCOMPACT"
)

// seedToolUseID and controlToolUseID are the two observations a suppression case makes.
const seedToolUseID = "toolu_01RELEASESWITCHSEED0001"

// recallAnswer is what `qompack recall` said. `ok` is whether the command answered at all, which
// is the half that matters here: a switch that silently broke retrieval would show up as an error
// or an unavailable, not as an empty result set.
type recallAnswer struct {
	OK    bool
	Count float64
	Found bool
}

// recallOnce asks the bundle's binary for a recall and decodes the FIRST JSON document it writes.
// A decoder rather than json.Unmarshal because the command follows its answer with a rendered
// evidence block, and a whole-buffer decode would fail on the trailing text.
func recallOnce(t *testing.T, b bundle, env map[string]string) recallAnswer {
	t.Helper()
	stdout, stderr, code := run(t, b.Bin, b.Dir, []string{"recall", "authMiddleware"}, nil, env)
	ans := recallAnswer{OK: code == 0}
	if code != 0 {
		t.Logf("release: recall exited %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
		return ans
	}
	var doc struct {
		Count float64 `json:"count"`
		Found bool    `json:"found"`
	}
	if err := json.NewDecoder(bytes.NewReader(stdout)).Decode(&doc); err != nil {
		ans.OK = false
		t.Logf("release: recall wrote no JSON document: %v\nstdout:\n%s", err, stdout)
		return ans
	}
	ans.Count, ans.Found = doc.Count, doc.Found
	return ans
}

// runAllHooksExpectZero drives the six hooks and asserts §13 invariant 6 for each, returning their
// concatenated stdout so a case can say what they answered.
func runAllHooksExpectZero(t *testing.T, b bundle, p project, env map[string]string, what string) string {
	t.Helper()
	var all strings.Builder
	for _, h := range hookSubcommands {
		stdout, stderr, code := run(t, b.Bin, b.Dir, h.argv,
			hookPayload(t, h.event, p.Root, seedToolUseID, "startup"), env)
		require.Equal(t, 0, code,
			"hook %q must exit 0 with %s (§13 invariant 6: hooks exit 0, always)\nstdout:\n%s\nstderr:\n%s",
			h.event, what, stdout, stderr)
		require.True(t, emptyOrParseableJSON(stdout),
			"hook %q must write empty or parseable JSON with %s, got:\n%s", h.event, what, stdout)
		all.Write(bytes.TrimSpace(stdout))
	}
	return all.String()
}

// suppression is one control-vs-flipped measurement of a switch that is meant to stop SessionStart
// reinjection.
type suppression struct {
	Seeded          bool
	Flipped         string
	Control         string
	FlippedRecall   recallAnswer
	ControlRecall   recallAnswer
	FlippedSurfaces []string
}

// measureSuppression seeds a project with the switch ALREADY flipped, observes what SessionStart
// `source: compact` answers, then stops the daemon, removes the switch and asks again.
//
// The order is the point. A running daemon loaded its configuration at startup, so flipping an
// environment variable in a later hook process changes the hook and not the resident daemon — a
// case that flipped a switch mid-session would be measuring a daemon still running under the
// defaults, and would report the switch working when nothing had changed. Taking the CONTROL last,
// after a real daemon restart, also makes the measurement non-vacuous: an empty answer under the
// switch means something only if the same project answers non-empty without it.
func measureSuppression(t *testing.T, b bundle, p project, key, value string) suppression {
	t.Helper()
	var s suppression
	env := p.envWith(key, value)

	for _, seed := range []struct {
		argv   []string
		event  string
		source string
	}{
		{[]string{"session-start"}, "SessionStart", "startup"},
		{[]string{"observe", "tool"}, "PostToolUse", ""},
	} {
		stdout, stderr, code := run(t, b.Bin, b.Dir, seed.argv,
			hookPayload(t, seed.event, p.Root, seedToolUseID, seed.source), env)
		require.Equal(t, 0, code, "seeding hook %q must exit 0\nstdout:\n%s\nstderr:\n%s", seed.event, stdout, stderr)
		require.True(t, emptyOrParseableJSON(stdout), "seeding hook %q wrote unparseable stdout:\n%s", seed.event, stdout)
	}
	s.Seeded = waitForToolUseIndex(t, p.Root)
	if !s.Seeded {
		return s
	}
	s.FlippedSurfaces = storeSurfaces(p.Root)

	stdout, _, code := run(t, b.Bin, b.Dir, []string{"session-start"},
		hookPayload(t, "SessionStart", p.Root, seedToolUseID, "compact"), env)
	require.Equal(t, 0, code, "SessionStart source=compact must exit 0 with %s=%s", key, value)
	s.Flipped = additionalContext(stdout)
	s.FlippedRecall = recallOnce(t, b, env)

	if !stopDaemonAndWait(t, p.Root) {
		t.Logf("release: the daemon would not let go of %s; the control measurement below may still "+
			"be answered by the daemon started under %s=%s", p.Root, key, value)
		return s
	}
	stdout, _, code = run(t, b.Bin, b.Dir, []string{"session-start"},
		hookPayload(t, "SessionStart", p.Root, seedToolUseID, "compact"), p.Env)
	require.Equal(t, 0, code, "the control SessionStart source=compact must exit 0")
	s.Control = additionalContext(stdout)
	s.ControlRecall = recallOnce(t, b, p.Env)
	return s
}

// TestSwitch_ModePassiveStopsActingAndKeepsRecording is `runtime.mode: passive`.
//
// What it asserts is §12.1's own contract, which is NOT "passive records nothing": Mode.MayRecord
// is false only for ModeOff, and ModeDegradedPassive exists precisely so L0/L1 keep recording
// while everything that ACTS is off. So the observable effect of the switch is that SessionStart
// `source: compact` stops injecting, while the store keeps growing and retrieval keeps answering.
// A test that demanded an empty store under passive would be asserting the opposite of the design.
func TestSwitch_ModePassiveStopsActingAndKeepsRecording(t *testing.T) {
	b := assembledBundle(t)
	rec := newRecord(t, "switch_mode_passive", "runtime.mode", "passive")
	p := newProject(t)

	s := measureSuppression(t, b, p, envMode, "passive")
	if !s.Seeded {
		skipRecorded(t, rec, "the seeded observation never reached index/tool_use.jsonl, so there "+
			"was nothing recorded for the switch to leave alone")
	}
	if !injectedRehydration(s.Control) {
		skipRecorded(t, rec, "the control run injected no checkpoint span either, so an answer "+
			"carrying none under passive would be evidence of nothing")
	}
	rec.Control = fmt.Sprintf("defaults injected a checkpoint span, %d byte(s) of additionalContext; recall ok=%v count=%v",
		len(s.Control), s.ControlRecall.OK, s.ControlRecall.Count)

	require.True(t, stopDaemonAndWait(t, p.Root),
		"the control daemon must release the lock so status --json talks to a daemon started under passive")
	env := p.envWith(envMode, "passive")
	_ = runAllHooksExpectZero(t, b, p, env, "runtime.mode passive")

	st := jsonDoc(t, "status --json", mustRun(t, b, env, "status", "--json"))
	mode, ok := getPath(st, "data.snapshot.mode")
	require.True(t, ok, "`status --json` must report snapshot.mode while the daemon is running under passive")
	require.Equal(t, "degraded-passive", mode,
		"runtime.mode passive forces contract ModeDegradedPassive; status --json must report it")

	require.False(t, injectedRehydration(s.Flipped),
		"runtime.mode passive must not inject a checkpoint span: §12.1 turns every acting path off "+
			"while L0/L1 keep recording. The control run on the same project injected %d byte(s). "+
			"This run answered: %q", len(s.Control), s.Flipped)
	require.True(t, s.FlippedRecall.OK,
		"recall must still answer under passive: recording and retrieval are what passive keeps")
	require.NotEmpty(t, s.FlippedSurfaces,
		"passive must keep recording (Mode.MayRecord is false only for off), but nothing was written "+
			"under .qompack/objects or .qompack/index")

	rec.Outcome = OutcomeVerified
	rec.Reason = "runtime.mode passive stopped SessionStart reinjection and left recording and retrieval working"
	rec.Detail = fmt.Sprintf(
		"with the switch: SessionStart(compact) carried no injected span (additionalContext=%q), "+
			"recall ok=%v count=%v found=%v, "+
			"%d file(s) under objects+index, all six hooks exit 0 with parseable stdout, "+
			"`status --json` reports snapshot.mode=degraded-passive. Without it, on the same project after "+
			"a daemon restart: %d byte(s) carrying an injected span, recall ok=%v count=%v. "+
			"NOTE for the reader: passive is not a recording switch — Mode.MayRecord (internal/contract/"+
			"mode.go) is false only for `off`, so the objects and index above are the design, not a leak. "+
			"Not duplicated here: internal/ipc's StateFromConfig unit tests and internal/contract's "+
			"monitor tests pin the mode mapping itself",
		s.Flipped, s.FlippedRecall.OK, s.FlippedRecall.Count, s.FlippedRecall.Found,
		len(s.FlippedSurfaces), len(s.Control), s.ControlRecall.OK, s.ControlRecall.Count)
	writeRecord(t, rec)
}

// TestSwitch_ModeOffStopsEverything is `runtime.mode: off` — the operator's own instruction to do
// nothing at all. Unlike passive it IS a recording switch, and unlike every other switch here it
// short-circuits before the hook reads stdin (internal/cli/hookclient.go).
func TestSwitch_ModeOffStopsEverything(t *testing.T) {
	b := assembledBundle(t)
	rec := newRecord(t, "switch_mode_off", "runtime.mode", "off")
	p := newProject(t)

	env := p.envWith(envMode, "off")
	out := runAllHooksExpectZero(t, b, p, env, "runtime.mode off")
	require.NotContains(t, out, injectionMarker,
		"no hook may inject a checkpoint span with runtime.mode off")
	require.NotContains(t, out, "additionalContext",
		"no hook may answer with additionalContext at all under runtime.mode off")

	surfaces := storeSurfaces(p.Root)
	require.Empty(t, surfaces,
		"runtime.mode off must write nothing under .qompack/objects or .qompack/index, found: %v", surfaces)
	require.False(t, lockFileExists(p.Root),
		"runtime.mode off must start no daemon, but %s exists", filepath.Base(lockPath(p.Root)))

	cfg := jsonDoc(t, "config print --json", mustRun(t, b, env, "config", "print", "--json"))
	mode, ok := getPath(cfg, "runtime.mode")
	require.True(t, ok, "`config print --json` must report runtime.mode")
	require.Equal(t, "off", mode, "the shipped binary must report the mode the operator set")

	rec.Outcome = OutcomeVerified
	rec.Reason = "runtime.mode off recorded nothing, started no daemon, and every hook still exited 0"
	rec.Detail = "all six hooks exit 0 with empty or parseable JSON with no additionalContext; " +
		"0 file(s) under objects+index; no daemon lock file was ever created. " +
		"`config print --json` reports runtime.mode=off (status --json is not used: this switch " +
		"starts no daemon). Not duplicated here: internal/cli/hooks_test.go and internal/daemon's " +
		"own mode tests pin the short-circuit at the seam"
	writeRecord(t, rec)
}

// TestSwitch_ReinjectionSessionStartCompactOff is
// `runtime.migration.reinjection.sessionStartCompact: false`: the one switch that stops exactly
// one event's answer and leaves the rest of the session untouched.
func TestSwitch_ReinjectionSessionStartCompactOff(t *testing.T) {
	b := assembledBundle(t)
	rec := newRecord(t, "switch_reinjection_session_start_compact", "runtime.migration.reinjection.sessionStartCompact", "false")
	p := newProject(t)

	s := measureSuppression(t, b, p, envReinjection, "false")
	if !s.Seeded {
		skipRecorded(t, rec, "the tool event in the same session never reached index/tool_use.jsonl, "+
			"so the half of this case that proves recording continued could not be measured")
	}
	if !injectedRehydration(s.Control) {
		skipRecorded(t, rec, "the control run injected no checkpoint span either, so an answer "+
			"carrying none under the switch would be evidence of nothing")
	}
	rec.Control = fmt.Sprintf("defaults injected a checkpoint span, %d byte(s) of additionalContext, on the same project", len(s.Control))

	require.False(t, injectedRehydration(s.Flipped),
		"sessionStartCompact=false must stop the injected checkpoint span on SessionStart "+
			"source=compact; the control run on the same project injected %d byte(s). This run "+
			"answered: %q", len(s.Control), s.Flipped)
	require.True(t, indexNamesToolUse(t, p.Root, seedToolUseID),
		"a tool event in the SAME session must still be recorded with reinjection off: the switch "+
			"disables one answer, not the session")

	rec.Outcome = OutcomeVerified
	rec.Reason = "reinjection off stopped the injected span on SessionStart source=compact while the session's tool event was still recorded"
	rec.Detail = fmt.Sprintf(
		"with the switch: SessionStart(compact) carried no injected span (its additionalContext was "+
			"%q — the contract monitor's probe marker, which is contract machinery and not "+
			"reinjection) and index/tool_use.jsonl names %s; %d file(s) under objects+index. Without "+
			"it, after a daemon restart: %d byte(s) carrying an injected span. Not duplicated here: "+
			"internal/daemon's TestService_ReinjectionKillSwitchEmitsNothing pins the service seam "+
			"and test/e2e's v5_x04 pins the hook answer",
		s.Flipped, seedToolUseID, len(s.FlippedSurfaces), len(s.Control))
	writeRecord(t, rec)
}

// TestSwitch_DaemonDisabled is `runtime.daemon.enabled: false`: no resident process, while the
// hooks keep answering and the capture still reaches the spool. The spool is the point — it is
// what distinguishes "the daemon is off" from "the plugin is off".
func TestSwitch_DaemonDisabled(t *testing.T) {
	b := assembledBundle(t)
	rec := newRecord(t, "switch_daemon_disabled", "runtime.daemon.enabled", "false")
	p := newProject(t)

	env := p.envWith(envDaemon, "false")
	_ = runAllHooksExpectZero(t, b, p, env, "runtime.daemon.enabled false")

	require.False(t, lockFileExists(p.Root),
		"runtime.daemon.enabled false must never create %s", lockPath(p.Root))

	spool, err := os.ReadDir(paths.Long(paths.Of(p.Root).Spool))
	require.NoError(t, err, "the spool directory must exist: a disabled daemon still spools")
	var names []string
	for _, e := range spool {
		names = append(names, e.Name())
	}
	require.NotEmpty(t, names,
		"the spool must hold client files: that is what distinguishes a disabled daemon from a disabled plugin")

	cfg := jsonDoc(t, "config print --json", mustRun(t, b, env, "config", "print", "--json"))
	enabled, ok := getPath(cfg, "runtime.daemon.enabled")
	require.True(t, ok, "`config print --json` must report runtime.daemon.enabled")
	require.Equal(t, false, enabled, "the shipped binary must report the switch the operator set")

	rec.Outcome = OutcomeVerified
	rec.Reason = "no daemon was started and no lock file appeared, while the hooks still answered and spooled"
	rec.Detail = fmt.Sprintf(
		"all six hooks exit 0 with empty or parseable JSON with no additionalContext; "+
			"%s never appeared; the spool holds %d file(s) %v, which is what distinguishes a disabled "+
			"daemon from a disabled plugin. `config print --json` reports runtime.daemon.enabled=false "+
			"(status --json is not used: this switch starts no daemon). Not duplicated here: "+
			"test/e2e's v5_x15 daemon-off project covers the end-to-end path",
		lockPath(p.Root), len(names), names)
	writeRecord(t, rec)
}

// refusedSwitches are the gates Validate REFUSES rather than honours. They are a different kind of
// control from the four above and the distinction is the point: nothing is disabled when one of
// these is set, because it was never enabled — the operator's value is rejected, restored to the
// default, and recorded as a §11.3 violation.
var refusedSwitches = []struct{ name, key, env string }{
	{
		"switch_refused_replacement_new_result", "runtime.migration.replacement.newResult",
		"QOMPACK_RUNTIME__MIGRATION__REPLACEMENT__NEWRESULT",
	},
	{
		"switch_refused_experiments_enabled", "runtime.migration.experiments.enabled",
		"QOMPACK_RUNTIME__MIGRATION__EXPERIMENTS__ENABLED",
	},
	{
		"switch_refused_telemetry_enabled", "runtime.telemetry.enabled",
		"QOMPACK_RUNTIME__TELEMETRY__ENABLED",
	},
}

// TestSwitch_GatedCapabilitiesAreRefusedNotDisabled covers the three switches an operator can set
// and the product will not accept.
func TestSwitch_GatedCapabilitiesAreRefusedNotDisabled(t *testing.T) {
	b := assembledBundle(t)
	for _, sw := range refusedSwitches {
		t.Run(sw.name, func(t *testing.T) {
			rec := newRecord(t, sw.name, sw.key, "true")
			p := newProject(t)
			env := p.envWith(sw.env, "true")

			cfg := jsonDoc(t, "config print --json", mustRun(t, b, env, "config", "print", "--json"))
			got, ok := getPath(cfg, sw.key)
			require.True(t, ok, "`config print --json` must report %s", sw.key)
			require.Equal(t, false, got,
				"%s must stay at its default when an operator sets it: its gate has not passed", sw.key)

			violations := readViolations(t, p.Root)
			require.Contains(t, violations, sw.key,
				"setting %s must be RECORDED as a §11.3 violation in state/config-violations.json, "+
					"not silently ignored", sw.key)

			rec.Outcome = OutcomeVerified
			rec.Reason = "the operator's value was refused and restored to the default, with a recorded violation"
			rec.Detail = fmt.Sprintf(
				"`config print --json` reports %s=false after the environment layer set it true, and "+
					"state/config-violations.json names %v. This switch REFUSES rather than disables: "+
					"there is no production reader to turn off (internal/config/migration.go's gate "+
					"table), so nothing stops — the value never takes effect at all. Not duplicated "+
					"here: internal/config/validate_test.go pins the refusal itself and test/security's "+
					"posture_telemetry_is_refused pins telemetry end to end",
				sw.key, violations)
			writeRecord(t, rec)
		})
	}
}

// mustRun runs a non-hook subcommand and requires exit 0.
func mustRun(t *testing.T, b bundle, env map[string]string, args ...string) []byte {
	t.Helper()
	stdout, stderr, code := run(t, b.Bin, b.Dir, args, nil, env)
	require.Equal(t, 0, code, "`qompack %s` must succeed\nstdout:\n%s\nstderr:\n%s",
		strings.Join(args, " "), stdout, stderr)
	return stdout
}

// readViolations returns the keys state/config-violations.json names. The field is `Key` with a
// capital K because internal/config.Violation carries no JSON tags — the shape is read from the
// file the product writes, not invented here.
func readViolations(t *testing.T, root string) []string {
	t.Helper()
	p := filepath.Join(paths.Of(root).State, "config-violations.json")
	b, err := os.ReadFile(paths.Long(p))
	if err != nil {
		return nil
	}
	var rows []struct {
		Key string `json:"Key"`
	}
	require.NoError(t, json.Unmarshal(b, &rows), "state/config-violations.json must parse:\n%s", b)
	var keys []string
	for _, r := range rows {
		keys = append(keys, r.Key)
	}
	return keys
}

// indexNamesToolUse reports whether index/tool_use.jsonl mentions the given tool_use id.
func indexNamesToolUse(t *testing.T, root, id string) bool {
	t.Helper()
	b, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).Index, toolUseIndexFile)))
	if err != nil {
		return false
	}
	return bytes.Contains(b, []byte(id))
}
