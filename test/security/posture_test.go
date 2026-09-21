package security

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// The posture half of §7.1 and §13 invariant 7: this plugin talks to nobody, and privacy-denied
// data follows privacy policy even when everything else is degrading toward pass-through.
//
// The no-network half of the posture is owned by test/guards' TestGuard_NoNetworkImports, which
// proves it of the import graph for the whole tree. It is CITED here rather than re-run: a second
// copy of that assertion would not make it truer, and the audit proposal names it as the authority.
// What this file adds is the two things a graph check cannot see — what the shipped binary does
// when an operator asks it to turn telemetry ON, and what it persists for a delivery privacy
// refuses.

const (
	// postureSession is the session this file's captures belong to.
	postureSession = core.SessionID("sess-security-posture-0001")
	// deniedCaptureToolUseID addresses the capture whose tool_input names a path outside the root.
	deniedCaptureToolUseID = "toolu_security_privacy_denied_01"
	// telemetryKey is the hardwired-off switch both posture cases set: the one value an operator
	// can never legitimately enable, so what is measured is only what a violation does.
	telemetryKey = "runtime.telemetry.enabled"
)

// TestSecurity_TelemetryCannotBeTurnedOn asks the packaged binary for its effective configuration
// after a project config has set runtime.telemetry.enabled to true.
func TestSecurity_TelemetryCannotBeTurnedOn(t *testing.T) {
	b := assembledBundle(t)
	p := newProject(t, "proj")
	t.Cleanup(func() { shutdownIfReachable(t, p.Root) })

	rec := newRecord(t, "posture_telemetry_is_refused")
	rec.Capability = CapPosture

	writeProjectConfig(t, p, map[string]any{
		"runtime": map[string]any{"telemetry": map[string]any{"enabled": true}},
	})

	stdout, stderr, code := run(t, b.Bin, p.Root, []string{"config", "print", "--json"}, nil, p.Env)
	require.Equal(t, 0, code, "config print must succeed\nstderr:\n%s", stderr)

	var cfg struct {
		Runtime struct {
			Telemetry struct {
				Enabled bool `json:"enabled"`
			} `json:"telemetry"`
		} `json:"runtime"`
	}
	require.NoError(t, json.Unmarshal(stdout, &cfg), "config print --json must emit a JSON document")
	require.False(t, cfg.Runtime.Telemetry.Enabled,
		"runtime.telemetry.enabled is hardwired off; a project config must not be able to set it")

	// The refusal must be VISIBLE, not silent: §11.3's typed violation list is where it lands.
	violations := readViolations(t, p.Root)

	if namesViolation(violations, telemetryKey) {
		rec.Outcome = OutcomeVerified
		rec.Reason = "a project config setting runtime.telemetry.enabled=true was refused: the " +
			"effective configuration reports false and state/config-violations.json names the key, " +
			"so the refusal is loud rather than silent (§7.1, §11.3)."
	} else {
		rec.Outcome = OutcomeFailed
		rec.Reason = "the effective configuration correctly reports runtime.telemetry.enabled=false, " +
			"but nothing recorded the refusal where an operator would find it: " +
			"state/config-violations.json does not name the key. Owner: internal/cli."
		t.Logf("RETURNED FINDING (owner internal/cli): `config print` refuses telemetry silently; "+
			"violations recorded: %v", violations)
	}
	rec.Detail = fmt.Sprintf("%d configuration violation(s) recorded", len(violations))
	writeRecord(t, rec)
}

// V6 replaces the previous dropped-path finding with a capture-time refusal.
// Historical failure artifacts retain the original identifier and observation.
func TestSecurity_OutOfProjectCaptureIsRefusedBeforePersistence(t *testing.T) {
	b := assembledBundle(t)
	base := tempBase(t)
	p := newProjectAt(t, base, "proj")
	t.Cleanup(func() { shutdownIfReachable(t, p.Root) })

	rec := newRecord(t, "posture_out_of_project_capture_is_archived")
	rec.Capability = CapPosture

	const outsideCaptureSentinel = "OUTSIDE-CAPTURE-SENTINEL-2d5b71"

	outsideDir := filepath.Join(base, "neighbour")
	require.NoError(t, os.MkdirAll(paths.Long(outsideDir), 0o700))
	outsideFile := filepath.Join(outsideDir, "secret.env")
	require.NoError(t, os.WriteFile(paths.Long(outsideFile),
		[]byte("NEIGHBOUR_VALUE="+outsideCaptureSentinel+"\n"), 0o600))

	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, postureSession))
	require.True(t, waitDaemonUp(t, p.Root), "session-start must bring a daemon up")

	stdout, stderr, code := run(t, b.Bin, p.Root, []string{"observe", "tool"},
		readToolPayload(t, p.Root, postureSession, deniedCaptureToolUseID,
			filepath.ToSlash(outsideFile), "NEIGHBOUR_VALUE="+outsideCaptureSentinel+"\n"),
		p.Env)
	require.Equal(t, 0, code, "a refused capture is still a hook: it exits 0\nstderr:\n%s", stderr)
	require.True(t, emptyOrParseableJSON(stdout), "a refused capture still writes a parseable document")

	runHook(t, b.Bin, p, []string{"flush"}, sessionEndPayload(t, p.Root, postureSession))
	shutdownIfReachable(t, p.Root)

	files := walkSurfaces(t, []surfaceRoot{{Dir: paths.Of(p.Root).Dot, SplitBySegment: true}})
	withContent := surfacesContaining(files, outsideCaptureSentinel)
	withPath := surfacesContaining(files, filepath.ToSlash(outsideFile))

	switch {
	case len(withContent) == 0 && len(withPath) == 0:
		rec.Outcome = OutcomeVerified
		rec.Reason = "a capture whose tool_input named a file outside the project root persisted " +
			"nothing: neither its bytes nor its absolute path reached any file under .qompack/, " +
			"and the hook still exited 0 with a host-parseable document."
	case len(withContent) == 0:
		rec.Outcome = OutcomeFailed
		rec.Reason = fmt.Sprintf("the out-of-project capture's BYTES were not persisted, but its "+
			"absolute path reached %v. A path outside the project is itself private information: it "+
			"names a directory the operator never opted in. Owner: internal/observer (tool-use "+
			"normalization) + internal/cli (the hook side).", withPath)
		t.Logf("RETURNED FINDING (owner internal/observer + internal/cli): the absolute "+
			"out-of-project path reached %v", withPath)
	default:
		rec.Outcome = OutcomeFailed
		rec.Reason = fmt.Sprintf("capture-time scope refusal failed: outside bytes reached %v; owner internal/cli + internal/daemon + internal/observer", withContent)
	}
	rec.Detail = fmt.Sprintf("swept %d files under .qompack/", len(files))
	writeRecord(t, rec)
	require.Equal(t, OutcomeVerified, rec.Outcome, rec.Reason)
}

// configViolation is the §11.3 record `config print` leaves behind when it refuses a value. It is
// re-declared here rather than imported so this package asserts on the JSON a shipped binary
// actually writes, which is what an operator or a support tool would read.
type configViolation struct {
	Key     string `json:"key"`
	Message string `json:"message"`
	Got     any    `json:"got"`
	Want    any    `json:"want"`
}

// readViolations reads state/config-violations.json, returning nothing when the file is absent.
func readViolations(t *testing.T, root string) []configViolation {
	t.Helper()
	b, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).State, "config-violations.json")))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var out []configViolation
	require.NoError(t, json.Unmarshal(b, &out), "state/config-violations.json must be readable JSON")
	return out
}

// namesViolation reports whether the §11.3 violation list names key, which is the product saying in
// its own voice that it refused that value rather than silently dropping it.
func namesViolation(violations []configViolation, key string) bool {
	for _, v := range violations {
		if v.Key == key {
			return true
		}
	}
	return false
}

// sortedKeys is the sorted key set of a string-keyed set, for a record's detail line.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestSecurity_AnyConfigurationViolationDisablesCapture was finding S-7, and since Task 6 it is the
// regression assertion for the fix. Its RECORD NAME is historical and describes the defect, not the
// product: the row is `verified` when capture survives a violation.
//
// The finding: config.LoadForCapture ended with `if len(cfg.Validate()) != 0 { return fail() }` —
// ANY violation, in any key, made the whole capture unavailable with core.ErrDegraded, and
// internal/cli's hook path then returned empty output before session-start reached its own daemon
// bootstrap. It was found by accident, by an attempt to force a Loud through the hardwired-off
// telemetry key that produced a session with no daemon, no objects and no index at all. The
// mechanism generalized finding S-2 well past the one key S-2 names.
//
// §11.3's contract for an invalid value is the opposite: clamp to the default and record the
// violation, which is what `config print` does through LoadConfigAndReport. The two loaders now
// agree — LoadForCapture runs the same per-leaf fallback and returns the violations, and the hook
// path records them in state/config-violations.json. A NEWER runtime.migration/runtime.phase7
// settingsVersion is handled the way Load handles it: the whole block is reset to this build's
// defaults and one Warning is recorded (§7.1 — degraded and reported, never guessed), so capture
// keeps running with every unknown future switch off.
//
// The key chosen here has nothing to do with capture or with bounds, which is the point: it is a
// value the operator cannot enable under any circumstances, so the only thing being measured is
// what a violation does.
func TestSecurity_AnyConfigurationViolationDisablesCapture(t *testing.T) {
	b := assembledBundle(t)
	p := newProject(t, "proj")
	t.Cleanup(func() { shutdownIfReachable(t, p.Root) })

	rec := newRecord(t, "posture_any_config_violation_disables_capture")
	rec.Capability = CapPosture

	writeProjectConfig(t, p, map[string]any{
		"runtime": map[string]any{"telemetry": map[string]any{"enabled": true}},
	})

	const marker = "ORDINARY-CAPTURE-UNDER-A-VIOLATION-8c41d9"
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, postureSession))
	daemonUp := waitDaemonUpFor(t, p.Root, absentDaemonBound)

	runHook(t, b.Bin, p, []string{"observe", "tool"},
		readToolPayload(t, p.Root, postureSession, "toolu_security_violation_01",
			"src/ordinary.ts", "// "+marker+"\n"))

	// The violation list as the HOOK path left it, read before anything else can add to it: this is
	// the count that says whether the hot loader recorded the refusal, and it is only that count
	// while `config print` — which writes the same file through the other loader — has not run yet.
	hookViolations := readViolations(t, p.Root)

	// What the product itself reports about the offending key, read the way an operator would read
	// it, and the same signal bounds_test's capture-cap row keys on. `config print` goes through
	// LoadConfigAndReport, so an effective value back at the default PLUS the key named in the
	// §11.3 list is the product stating that it clamped and recorded rather than guessed.
	printed, _, printCode := run(t, b.Bin, p.Root, []string{"config", "print", "--json"}, nil, p.Env)
	require.Equal(t, 0, printCode, "config print must succeed")
	var cfgDoc struct {
		Runtime struct {
			Telemetry struct {
				Enabled bool `json:"enabled"`
			} `json:"telemetry"`
		} `json:"runtime"`
	}
	require.NoError(t, json.Unmarshal(printed, &cfgDoc))

	runHook(t, b.Bin, p, []string{"flush"}, sessionEndPayload(t, p.Root, postureSession))
	shutdownIfReachable(t, p.Root)

	files := walkSurfaces(t, []surfaceRoot{{Dir: paths.Of(p.Root).Dot, SplitBySegment: true}})
	captured := len(surfacesContaining(files, marker)) > 0
	named := namesViolation(readViolations(t, p.Root), telemetryKey)

	// The verdict is keyed on what the product reports and on what survived, NOT on daemonUp.
	// daemonUp is a wall-clock observation bounded by absentDaemonBound, so keying the verified
	// branch on it would let a cold start slower than that bound write a `failed` record claiming
	// S-7 is still open on a build that had fixed it. It stays in the detail line as the diagnostic
	// it is.
	reported := !cfgDoc.Runtime.Telemetry.Enabled && named

	rec.Detail = fmt.Sprintf("config print reports %s=%v and the §11.3 list names it afterwards=%v; "+
		"violations recorded by the hook path alone=%d; ordinary capture retained=%v; daemon "+
		"reachable within %s=%v", telemetryKey, cfgDoc.Runtime.Telemetry.Enabled, named,
		len(hookViolations), captured, absentDaemonBound, daemonUp)

	if reported && captured {
		rec.Outcome = OutcomeVerified
		rec.Reason = "an invalid configuration key was clamped and reported — `config print` reports " +
			"the default and state/config-violations.json names the key — and an ordinary capture " +
			"under the same configuration was still retained."
	} else {
		rec.Outcome = OutcomeFailed
		rec.Reason = "one invalid configuration key — a hardwired-off switch an operator can never " +
			"legitimately enable — silently disabled the product. config.LoadForCapture refuses the " +
			"whole delivery when Validate() reports anything at all, so internal/cli's hook path " +
			"returns empty output before session-start reaches its daemon bootstrap and the ordinary " +
			"capture never lands; the detail line above says what survived. §11.3's contract for an " +
			"invalid value is clamp-and-record, " +
			"which is what `config print` does through LoadConfigAndReport, so the two loaders " +
			"disagree and the hot one fails closed over the entire product rather than over the key. " +
			"Clamping is safe here for the reason the cap already relies on: the next use of the " +
			"value is bounded again by internal/cli's own hookCaptureLimit. This generalizes S-2 to " +
			"every validated key. Owner: internal/config + internal/cli. RETURNED, not fixed here."
		t.Logf("RETURNED FINDING (owner internal/config + internal/cli): one config violation "+
			"disabled capture entirely (clamped and recorded by `config print`=%v, capture "+
			"retained=%v, daemon up=%v)", reported, captured, daemonUp)
	}
	writeRecord(t, rec)
}
