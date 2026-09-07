// Package canary holds SP-19 M0-03's host-contract canaries: the bounded, disposable-target probes
// that decide whether a capability the register claims is actually supported by an installed host.
//
// It exists because a repository validator cannot certify installed behaviour, and neither can a
// document. plans/MIGRATION-EVIDENCE.md B01 blocks target certification until "actual packaged hook
// canaries in disposable sessions" exist; M0-G2 requires "mechanism plus target canary artifact
// including host/provider/OS/version/date"; and M0-G4 requires repository-only validation to be
// LABELLED as such. These tests are that machinery.
//
// # What a canary is allowed to do here
//
// M0-03 is explicit: "Never conduct destructive/recovery capability probes in the active user
// session." This package therefore never starts a Claude session, never installs, enables or
// disables a plugin, and never provokes compaction, blocking or result replacement. What it does is
// read-only inventory (`claude --version`, `claude plugin validate`), and exercising THIS
// repository's own built binary in a temporary project. Every probe that would need a live host
// session skips with a recorded reason unless a disposable target is supplied through the
// environment:
//
//	QOMPACK_CANARY_ARTIFACTS   directory to write canary records into (default: the test's temp dir)
//	QOMPACK_CANARY_TARGET      set when a disposable target has been provided; without it the
//	                           optimization canaries record `skipped`, never `verified`
//	QOMPACK_CANARY_TRANSCRIPT  path to a disposable session transcript to scan for a sentinel
//	QOMPACK_CANARY_SENTINEL    the sentinel token that transcript should contain
//
// A skipped canary is not a passing one. It leaves its capability unverified and says why, which is
// the whole distinction SP-19's evidence-status vocabulary exists to preserve.
//
// # Privacy
//
// A record may contain: the sentinel token, hook event names, host and plugin versions, exit codes,
// byte counts and durations. It may NOT contain transcript text, prompt text, tool payloads, or any
// path under the user's home other than a temporary directory this test created. Records are
// written as JSON so that boundary is inspectable rather than asserted.
//
// canary is a composition root (§3.2): it imports the tree and nothing may import it.
package canary

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// Outcome is what a canary establishes. It is deliberately NOT a boolean: "we could not run this"
// and "this failed" are different facts about a capability, and collapsing them is how an unrun
// probe becomes evidence of support.
type Outcome string

// The four outcomes.
const (
	// OutcomeVerified: the probe ran against its target and the mechanism did what it promised.
	OutcomeVerified Outcome = "verified"
	// OutcomeDegraded: the probe ran and the mechanism behaved acceptably but not as specified —
	// a fallback engaged, a bound was missed, a shape differed. Reason says which.
	OutcomeDegraded Outcome = "degraded"
	// OutcomeSkipped: the probe did not run. The capability stays unverified and Reason says what
	// was missing.
	OutcomeSkipped Outcome = "skipped"
	// OutcomeFailed: the probe ran and the mechanism did not honour its contract.
	OutcomeFailed Outcome = "failed"
)

// Scope is how far a canary reached, recorded structurally so that a verified outcome cannot be
// misread as installed-host certification: a probe of this build's own binary says nothing about
// the host, a read-only CLI invocation says nothing about a session, and only a disposable
// installed-host session can verify an acting or optimizing capability (M0-G2, B01).
type Scope string

// The three scopes.
const (
	// ScopeRepository: this build's own binary or committed bundle, exercised locally.
	ScopeRepository Scope = "repository"
	// ScopeInstalledCLI: a read-only invocation of the installed Claude CLI (`--version`,
	// `plugin validate`). It names the host; it starts no session.
	ScopeInstalledCLI Scope = "installed_cli"
	// ScopeInstalledSession: a disposable installed-host session. The only scope whose verified
	// outcome may upgrade a capability's evidence status.
	ScopeInstalledSession Scope = "installed_session"
)

// Record is one canary's retained artifact: what was probed, against which host, what happened and
// why. It is the "target canary artifact" M0-G2 requires, and its field set is the privacy boundary
// described in this package's doc comment.
type Record struct {
	Name       string              `json:"name"`
	Capability contract.Capability `json:"capability"`
	// Scope is required: writeRecord refuses a record that does not say how far it reached.
	Scope   Scope   `json:"scope"`
	Outcome Outcome `json:"outcome"`
	// Reason is prose, and it is the point: a skipped or degraded canary that does not say why is
	// indistinguishable from one nobody ran.
	Reason string          `json:"reason"`
	Target contract.Target `json:"target"`
	// Artifact names a companion file, when one exists (a CLI's own JSON output, say).
	Artifact string `json:"artifact,omitempty"`
	// RecordedAt is an RFC3339 UTC timestamp. Evidence without a date cannot be aged out.
	RecordedAt string `json:"recorded_at"`
}

// hostOnce caches the one `claude --version` this package runs, so a suite of canaries does not
// spawn the CLI once per test.
var (
	hostOnce    sync.Once
	hostVersion string
	hostFound   bool
)

// claudeVersion returns the installed Claude CLI's version string, and whether the CLI was found on
// PATH at all. It runs `claude --version`, which is read-only: it starts no session, reads no
// project and changes no configuration.
func claudeVersion() (string, bool) {
	hostOnce.Do(func() {
		bin, err := exec.LookPath("claude")
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), claudeProbeBound)
		defer cancel()
		out, err := exec.CommandContext(ctx, bin, "--version").Output() //nolint:gosec // G204: fixed argument, path from LookPath
		if err != nil {
			return
		}
		// The CLI answers "2.1.263 (Claude Code)"; the leading token is the version, and the rest
		// is a product name already carried by Target.Provider. Keeping both would make every
		// rendered target read "claude-code 2.1.263 (Claude Code)".
		raw := strings.TrimSpace(string(out))
		hostVersion, hostFound = raw, true
		if fields := strings.Fields(raw); len(fields) > 0 {
			hostVersion = fields[0]
		}
	})
	return hostVersion, hostFound
}

// claudeProbeBound bounds every read-only Claude CLI invocation. A canary that hangs on an
// unresponsive CLI would block the suite it is meant to inform.
const claudeProbeBound = 60 * time.Second

// hostTarget returns the target this run's evidence is attributed to. An absent CLI yields provider
// "unknown" rather than a guess: M0-G2 wants the host named, and "we could not tell" is a legitimate
// answer that must not be dressed up as "claude-code, version unknown".
func hostTarget(t *testing.T) contract.Target {
	t.Helper()

	tgt := contract.Target{
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
		Date:     time.Now().UTC().Format(time.RFC3339),
	}
	if v, ok := claudeVersion(); ok {
		tgt.Provider = "claude-code"
		tgt.Version = v
	} else {
		tgt.Provider = "unknown"
	}
	return tgt
}

// artifactDir returns where records are written: $QOMPACK_CANARY_ARTIFACTS when it is set (so a
// future authorized run can collect them), else the test's own temp directory (so an ordinary
// `go test` leaves nothing behind).
func artifactDir(t *testing.T) string {
	t.Helper()
	if dir := os.Getenv("QOMPACK_CANARY_ARTIFACTS"); dir != "" {
		if err := os.MkdirAll(paths.Long(dir), 0o700); err != nil {
			t.Fatalf("canary: creating the artifact directory %s: %v", dir, err)
		}
		return dir
	}
	return t.TempDir()
}

// writeRecord persists rec as JSON and returns the path it was written to, logging it so the path
// appears in `go test -v` output whether or not the artifact directory is collected.
//
// It fills RecordedAt when the caller has not, so no record can be written undated.
func writeRecord(t *testing.T, rec Record) string {
	t.Helper()

	if rec.Scope == "" {
		t.Fatalf("canary %s: a record must say how far it reached (Scope)", rec.Name)
	}
	// Only a disposable installed-host session can verify a capability that acts on or optimizes
	// the session; a repository- or CLI-scope probe can verify observation of this build alone.
	if rec.Outcome == OutcomeVerified && rec.Scope != ScopeInstalledSession && rec.Capability != contract.CapObservation {
		t.Fatalf("canary %s: %s cannot be verified at scope %q; only an installed session can", rec.Name, rec.Capability, rec.Scope)
	}
	if rec.RecordedAt == "" {
		rec.RecordedAt = time.Now().UTC().Format(time.RFC3339)
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatalf("canary: marshalling the %s record: %v", rec.Name, err)
	}
	path := filepath.Join(artifactDir(t), rec.Name+".json")
	if err := os.WriteFile(paths.Long(path), append(b, '\n'), 0o600); err != nil {
		t.Fatalf("canary: writing %s: %v", path, err)
	}
	t.Logf("canary %s: %s — %s\n  target:   %s\n  artifact: %s",
		rec.Name, rec.Outcome, rec.Reason, rec.Target, path)
	return path
}

// skipRecorded writes rec with Outcome skipped and the given reason, THEN skips the test.
//
// The order is the contract: a canary that skips without leaving a record is indistinguishable from
// one that was never written, and M0-G2's "unsupported/unknown optimizations remain off" needs the
// skip to be visible evidence rather than a silent absence.
func skipRecorded(t *testing.T, rec Record, reason string) {
	t.Helper()
	rec.Outcome = OutcomeSkipped
	rec.Reason = reason
	writeRecord(t, rec)
	// devtool lint's stubskips sub-check permits exactly three skip reasons (Rule W-1, Rule W-2,
	// or "platform: " followed by a reason). A canary without its disposable target is gated by
	// the host environment it runs in, which is what the platform category is for.
	t.Skip(platformSkipPrefix + "canary " + rec.Name + " skipped: " + reason)
}

// platformSkipPrefix is the stubskips-permitted prefix for an environment-gated skip
// (tools/devtool/stubskips.go).
const platformSkipPrefix = "platform: "

// buildOnce guards the single `go build` this package performs, for the same reason test/e2e does:
// a Go build is by far the most expensive thing here, and rebuilding per test would buy no coverage.
var (
	buildOnce sync.Once
	builtBin  string
	buildDir  string
	buildErr  error
)

// buildQompack compiles ./cmd/qompack into a temporary directory and returns the executable's path.
// The first caller pays for the build; every later caller gets the same path.
//
// This mirrors test/e2e/harness.go rather than importing it: test/e2e is a composition root, and
// nothing may import a composition root (§3.2).
func buildQompack(t *testing.T) string {
	t.Helper()
	buildOnce.Do(doBuild)
	if buildErr != nil {
		t.Fatalf("canary: building ./cmd/qompack: %v", buildErr)
	}
	return builtBin
}

// doBuild is buildQompack's body. The output directory outlives the test that triggered the build,
// so it is os.MkdirTemp rather than t.TempDir; removeBuild, called from TestMain, takes it away.
//
// The build deliberately inherits the real process environment. A `go build` running under a
// temp-directory HOME resolves a different module and build cache and can spend minutes
// re-downloading what is already on disk.
func doBuild() {
	root, err := moduleRoot()
	if err != nil {
		buildErr = err
		return
	}

	buildDir, buildErr = os.MkdirTemp("", "qompack-canary-")
	if buildErr != nil {
		return
	}

	out := filepath.Join(buildDir, "qompack")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}

	cmd := exec.CommandContext(context.Background(), "go", "build", "-o", out, "./cmd/qompack")
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		buildErr = fmt.Errorf("go build -o %s ./cmd/qompack (in %s): %w\n%s", out, root, err, stderr.String())
		return
	}
	builtBin = out
}

// removeBuild deletes the directory doBuild created. TestMain calls it after the last test.
func removeBuild() {
	if buildDir != "" {
		_ = os.RemoveAll(buildDir)
	}
}

// moduleRoot returns the repository root: the nearest ancestor of the working directory holding a
// go.mod. `go build ./cmd/qompack` is only meaningful from there, and a test's working directory is
// always its own package directory.
func moduleRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for d := wd; ; {
		if fi, statErr := os.Stat(filepath.Join(d, "go.mod")); statErr == nil && fi.Mode().IsRegular() {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", fmt.Errorf("no go.mod found in %s or any parent directory", wd)
		}
		d = parent
	}
}

// repoRoot is moduleRoot for a caller that would rather fail the test than handle the error.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := moduleRoot()
	if err != nil {
		t.Fatalf("canary: %v", err)
	}
	return root
}

// tempProject creates a disposable project root and returns it with the environment a spawned
// qompack process needs to stay inside it.
//
// Two decisions are load-bearing.
//
// It uses os.MkdirTemp rather than t.TempDir: t.TempDir fails the test if its cleanup cannot remove
// the tree, and on Windows a process still holding a file makes that certain. A canary's job is to
// report what the host did, not to fail over its own housekeeping, so removal is best-effort.
//
// It writes a state.bin with the daemon DISABLED. The hook client's Send spools instead of dialling
// when DaemonEnabled is false (internal/ipc/client.go), so exercising a hook here never spawns a
// detached daemon into a temporary tree. That keeps every canary hermetic — and it is honest about
// what the canary then covers: the hook adapter's payload handling and its exit contract, not the
// daemon behind it. Every record that uses this says so in its reason.
func tempProject(t *testing.T) (root string, env map[string]string) {
	t.Helper()

	base, err := os.MkdirTemp("", "qompack-canary-project-")
	if err != nil {
		t.Fatalf("canary: creating a temporary project: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })

	root = filepath.Join(base, "project")
	home := filepath.Join(base, "home")
	for _, d := range []string{root, home} {
		if err := os.MkdirAll(paths.Long(d), 0o700); err != nil {
			t.Fatalf("canary: creating %s: %v", d, err)
		}
	}
	if err := paths.EnsureLayout(paths.Of(root)); err != nil {
		t.Fatalf("canary: creating the .qompack layout under %s: %v", root, err)
	}

	st := ipc.StateFromConfig(config.Defaults())
	st.Mode = contract.ModeFull
	st.DaemonEnabled = false
	if err := ipc.WriteState(root, st); err != nil {
		t.Fatalf("canary: writing the daemon-disabled state for %s: %v", root, err)
	}

	return root, map[string]string{
		"QOMPACK_PROJECT_ROOT": root,
		"HOME":                 home,
		"USERPROFILE":          home,
	}
}

// runBinBound is how long one spawned qompack invocation may take. The longest manifest hook
// timeout is 20 s; anything past this is a hang, not a slow hook.
const runBinBound = 60 * time.Second

// runBin executes bin with args, feeding it stdin, and returns its stdout, stderr and exit code.
// env entries are layered on top of the process environment, so a caller overrides only what it
// names.
//
// A failure to START the process fails the test. A non-zero exit is RETURNED, never asserted here:
// the exit code is exactly what these canaries exist to observe.
func runBin(t *testing.T, bin string, args []string, stdin []byte, env map[string]string) (stdout, stderr []byte, code int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), runBinBound)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // G204: a binary this package built, with arguments it chose
	// A neutral working directory that is not the repository, so a root resolution that fell back
	// to the process cwd could not reach this checkout.
	cmd.Dir = filepath.Dir(bin)
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		code = 0
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		t.Fatalf("canary: could not run %s %v: %v\nstderr:\n%s", bin, args, err, errBuf.String())
	}
	return outBuf.Bytes(), errBuf.Bytes(), code
}

// emptyOrParseableJSON reports whether stdout is something a host could safely consume: either
// nothing at all, or one JSON document. §2.3's always-exit-0 rule is only half the hook contract —
// a hook that exits 0 while writing garbage to stdout breaks the host just as thoroughly.
func emptyOrParseableJSON(stdout []byte) bool {
	trimmed := bytes.TrimSpace(stdout)
	if len(trimmed) == 0 {
		return true
	}
	return json.Valid(trimmed)
}

// hookPayload renders a minimal, well-formed hook payload for event and session.
func hookPayload(t *testing.T, event, session, cwd string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"hook_event_name": event,
		"session_id":      session,
		"cwd":             cwd,
		"tool_name":       "Read",
		"tool_input":      map[string]any{"file_path": "canary.txt"},
		"tool_response":   "canary",
	})
	if err != nil {
		t.Fatalf("canary: marshalling a %s payload: %v", event, err)
	}
	return b
}

// disposableTarget reports whether a disposable target has been supplied for probes that would
// otherwise have to touch a live host. Without it, those canaries skip.
func disposableTarget() (string, bool) {
	v := os.Getenv("QOMPACK_CANARY_TARGET")
	return v, v != ""
}
