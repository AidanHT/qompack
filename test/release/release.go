// Package release is SP-17's independent-switch matrix: the tests that decide, through the
// artifact this repository actually ships, whether each kill switch stops its own feature and
// leaves every other capability working.
//
// # Why it is a package of its own
//
// Qompack.md v1.5 §7.1 promises that "recording, reinjection, output replacement and experimental
// policy have independent future kill switches". Independence is the claim, and independence is
// exactly what a per-package unit test cannot establish: internal/daemon's own test can show that
// the reinjection switch makes the rehydrate service answer empty, and internal/config's can show
// that Validate refuses `runtime.telemetry.enabled`, but neither can show that flipping ONE of
// them leaves the other four capabilities running in the product a user installs. That requires
// one switch flipped at a time, everything else at its default, driven through the bundle's own
// binary — which is what every case below does.
//
// The existing coverage is deliberately NOT duplicated. internal/daemon's
// TestService_ReinjectionKillSwitchEmitsNothing pins the reinjection switch at the service seam;
// test/e2e's v5_x15 pins a daemon-disabled project end to end; internal/config's validate_test
// pins the refusal of every gated leaf; test/security's posture_telemetry_is_refused pins
// telemetry. This package adds the one thing none of them can: the same switches read out of the
// SHIPPED bundle, with a control measurement taken on the same project under defaults, so a
// switch that appears to work because nothing worked is distinguishable from one that works.
//
// # Outcomes
//
// Three-valued, test/platform's vocabulary for test/platform's reason: `verified` (the case ran
// and the switch did what it promises), `failed` (it ran and it did not — a returned finding), and
// `skipped` (the case did not run, with the reason). A control measurement that produced nothing
// to compare against is a SKIP, never a pass: a reinjection switch "verified" against a session
// that had no injection to suppress would be evidence of nothing.
//
// release is a composition root (00-ARCHITECTURE.md §3.2), for the same reason test/platform is:
// it assembles a real bundle, drives that bundle's binary, and speaks admin IPC to the daemon it
// started. Nothing imports it back.
package release

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/testutil"
)

// Outcome is what one switch case established.
type Outcome string

// The three outcomes.
const (
	OutcomeVerified Outcome = "verified"
	OutcomeFailed   Outcome = "failed"
	OutcomeSkipped  Outcome = "skipped"
)

// TargetInfo is the platform a record is attributed to.
type TargetInfo struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
	Go   string `json:"go"`
}

// BundleInfo identifies the artifact the case drove.
type BundleInfo struct {
	Version      string `json:"version"`
	BinarySHA256 string `json:"binary_sha256"`
}

// Record is one switch case's retained artifact. `switch` and `value` name exactly what was
// flipped, so a reader never has to infer it from the case name.
type Record struct {
	Name       string     `json:"name"`
	Capability string     `json:"capability"`
	Scope      string     `json:"scope"`
	Switch     string     `json:"switch"`
	Value      string     `json:"value"`
	Target     TargetInfo `json:"target"`
	Bundle     BundleInfo `json:"bundle"`
	Outcome    Outcome    `json:"outcome"`
	Reason     string     `json:"reason"`
	Detail     string     `json:"detail,omitempty"`
	// Control is what the same project did with the switch at its default. A switch record with
	// no control is a record that cannot tell "the feature stopped" from "the feature never ran".
	Control string `json:"control,omitempty"`
}

const (
	// artifactsEnvKey names the directory records are collected into; unset means a temp dir, so
	// an ordinary `go test` leaves nothing behind.
	artifactsEnvKey = "QOMPACK_RELEASE_ARTIFACTS"
	artifactIndex   = "INDEX.json"
	// releaseSkipPrefix is the stubskips-permitted prefix for an environment-gated skip.
	releaseSkipPrefix = "platform: "
	// switchCapability is the capability every record here carries, so release-scope's
	// SP17-M7-06 rule can find them without reading prose.
	switchCapability = "switch"
	// installedBundleScope says these answers came from the shipped artifact, not the repository.
	installedBundleScope = "installed_bundle"
)

var (
	collectOnce sync.Once
	collectDir  string
	collectErr  error

	tempArtifactMu   sync.Mutex
	tempArtifactDirs = map[string]string{}

	writtenMu      sync.Mutex
	writtenRecords []string
)

// artifactDir returns where records are written.
func artifactDir(t *testing.T) string {
	t.Helper()
	if os.Getenv(artifactsEnvKey) != "" {
		collectOnce.Do(prepareCollectDir)
		if collectErr != nil {
			t.Fatalf("release: %v", collectErr)
		}
		return collectDir
	}
	tempArtifactMu.Lock()
	defer tempArtifactMu.Unlock()
	if d, ok := tempArtifactDirs[t.Name()]; ok {
		return d
	}
	d := t.TempDir()
	tempArtifactDirs[t.Name()] = d
	return d
}

// prepareCollectDir creates the collecting directory and removes the JSON an earlier run left, so
// a case that fatals before writing shows up as an absence rather than as a stale `verified`.
func prepareCollectDir() {
	dir := os.Getenv(artifactsEnvKey)
	if err := os.MkdirAll(paths.Long(dir), 0o700); err != nil {
		collectErr = fmt.Errorf("creating the artifact directory %s: %w", dir, err)
		return
	}
	entries, err := os.ReadDir(paths.Long(dir))
	if err != nil {
		collectErr = fmt.Errorf("reading the artifact directory %s: %w", dir, err)
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if err := os.Remove(paths.Long(filepath.Join(dir, e.Name()))); err != nil {
			collectErr = fmt.Errorf("pruning the stale record %s: %w", e.Name(), err)
			return
		}
	}
	collectDir = dir
}

// writeArtifactIndex records which names this run produced. TestMain calls it after the last case.
func writeArtifactIndex() {
	if os.Getenv(artifactsEnvKey) == "" || collectDir == "" {
		return
	}
	writtenMu.Lock()
	names := append([]string(nil), writtenRecords...)
	writtenMu.Unlock()
	sort.Strings(names)

	doc := struct {
		Target  TargetInfo `json:"target"`
		Bundle  BundleInfo `json:"bundle"`
		Records []string   `json:"records"`
	}{Target: hostTarget(), Bundle: BundleInfo{Version: hostBundle.Version, BinarySHA256: hostBundle.BinarySHA256}, Records: names}

	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(paths.Long(filepath.Join(collectDir, artifactIndex)), append(b, '\n'), 0o600)
}

// newRecord returns a record pre-filled with this host's target, the bundle's identity and the
// switch it is about.
func newRecord(t *testing.T, name, key, value string) Record {
	t.Helper()
	b := assembledBundle(t)
	return Record{
		Name: name, Capability: switchCapability, Scope: installedBundleScope,
		Switch: key, Value: value, Target: hostTarget(),
		Bundle: BundleInfo{Version: b.Version, BinarySHA256: b.BinarySHA256},
	}
}

// writeRecord persists rec as JSON under artifactDir.
func writeRecord(t *testing.T, rec Record) string {
	t.Helper()
	if rec.Outcome == "" {
		t.Fatalf("release %s: a record must carry an outcome", rec.Name)
	}
	if rec.Name == "" || rec.Name != filepath.Base(rec.Name) || strings.ContainsAny(rec.Name, `/\`) {
		t.Fatalf("release: record name %q must be a single path segment", rec.Name)
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatalf("release: marshalling the %s record: %v", rec.Name, err)
	}
	p := filepath.Join(artifactDir(t), rec.Name+".json")
	if err := os.WriteFile(paths.Long(p), append(b, '\n'), 0o600); err != nil {
		t.Fatalf("release: writing %s: %v", p, err)
	}
	writtenMu.Lock()
	writtenRecords = append(writtenRecords, rec.Name)
	writtenMu.Unlock()
	t.Logf("release %s: %s — %s\n  control:  %s\n  detail:   %s\n  artifact: %s",
		rec.Name, rec.Outcome, rec.Reason, rec.Control, rec.Detail, p)
	return p
}

// skipRecorded writes rec as `skipped` with the given reason, THEN skips. The order is the
// contract: a case that skips without leaving a record is indistinguishable from one nobody wrote.
func skipRecorded(t *testing.T, rec Record, reason string) {
	t.Helper()
	rec.Outcome = OutcomeSkipped
	rec.Reason = reason
	writeRecord(t, rec)
	t.Skip(releaseSkipPrefix + rec.Name + ": " + reason)
}

// hostTarget is the platform every record this run writes is attributed to.
func hostTarget() TargetInfo {
	return TargetInfo{OS: runtime.GOOS, Arch: runtime.GOARCH, Go: runtime.Version()}
}

// ---------------------------------------------------------------------------
// The assembled bundle under test
// ---------------------------------------------------------------------------

// bundleAssembleBound is how long `devtool bundle` may take on a loaded shared machine.
const bundleAssembleBound = 15 * time.Minute

// bundle is the assembled host-target bundle every case drives.
type bundle struct {
	Dir          string
	Bin          string
	Version      string
	BinarySHA256 string
}

var (
	bundleOnce sync.Once
	hostBundle bundle
	bundleBase string
	bundleErr  error
)

// assembledBundle assembles the host target's bundle once per test binary. Once, because it is a
// real compile plus a hash of every file, and nothing any case here mutates it.
func assembledBundle(t *testing.T) bundle {
	t.Helper()
	bundleOnce.Do(doAssemble)
	if bundleErr != nil {
		t.Fatalf("release: assembling the host bundle: %v", bundleErr)
	}
	return hostBundle
}

// doAssemble runs `go run ./tools/devtool bundle --target <host> --out <tmp>`.
func doAssemble() {
	repo, err := moduleRoot()
	if err != nil {
		bundleErr = err
		return
	}
	bundleBase, bundleErr = os.MkdirTemp("", "qompack-release-bundle-")
	if bundleErr != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), bundleAssembleBound)
	defer cancel()

	target := runtime.GOOS + "/" + runtime.GOARCH
	cmd := exec.CommandContext(ctx, "go", "run", "./tools/devtool", "bundle", //nolint:gosec // G204: fixed argv over this package's own temp directory
		"--target", target, "--out", bundleBase)
	cmd.Dir = repo
	stdio, cleanup, err := openChildStdio(nil, "qompack-release-assemble-stdio-")
	if err != nil {
		bundleErr = err
		return
	}
	defer cleanup()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdio.in, stdio.out, stdio.err
	cmd.WaitDelay = childWaitDelay
	err = cmd.Run()
	_, stderr := stdio.readBytes()
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil
	}
	if err != nil {
		bundleErr = fmt.Errorf("go run ./tools/devtool bundle --target %s --out %s: %w\nstderr:\n%s",
			target, bundleBase, err, stderr)
		return
	}
	dir, err := soleBundleDir(bundleBase)
	if err != nil {
		bundleErr = err
		return
	}
	var id struct {
		Version string `json:"version"`
		Target  struct {
			OS   string `json:"os"`
			Arch string `json:"arch"`
		} `json:"target"`
	}
	raw, err := os.ReadFile(paths.Long(filepath.Join(dir, "BUNDLE.json")))
	if err != nil {
		bundleErr = fmt.Errorf("reading the assembled BUNDLE.json: %w", err)
		return
	}
	if err := json.Unmarshal(raw, &id); err != nil {
		bundleErr = fmt.Errorf("parsing the assembled BUNDLE.json: %w", err)
		return
	}
	if id.Target.OS != runtime.GOOS || id.Target.Arch != runtime.GOARCH {
		bundleErr = fmt.Errorf("the assembled bundle names target %s/%s, not the host %s/%s",
			id.Target.OS, id.Target.Arch, runtime.GOOS, runtime.GOARCH)
		return
	}
	bin := filepath.Join(dir, "bin", "qompack"+exeSuffix())
	sum, err := fileSHA256(bin)
	if err != nil {
		bundleErr = fmt.Errorf("hashing the bundled binary: %w", err)
		return
	}
	hostBundle = bundle{Dir: dir, Bin: bin, Version: id.Version, BinarySHA256: sum}
}

// soleBundleDir returns the one directory the assembly produced, so a stale directory from an
// earlier run can never be picked up as the artifact under test.
func soleBundleDir(base string) (string, error) {
	entries, err := os.ReadDir(paths.Long(base))
	if err != nil {
		return "", fmt.Errorf("reading the bundle output directory %s: %w", base, err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) != 1 {
		return "", fmt.Errorf("expected exactly one bundle directory under %s, found %d: %v", base, len(dirs), dirs)
	}
	return filepath.Join(base, dirs[0]), nil
}

// removeBundle deletes the directory doAssemble created. TestMain calls it after the last test.
func removeBundle() {
	if bundleBase != "" {
		_ = os.RemoveAll(paths.Long(bundleBase))
	}
}

// exeSuffix is the extension the host's executables carry.
func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// moduleRoot returns the repository root: the nearest ancestor holding a go.mod.
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

// fileSHA256 returns the hex digest of a file's contents.
func fileSHA256(p string) (string, error) {
	b, err := os.ReadFile(paths.Long(p))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// ---------------------------------------------------------------------------
// Running the bundled binary
// ---------------------------------------------------------------------------

// runBound is how long one spawned qompack invocation may take.
const runBound = 120 * time.Second

// childWaitDelay bounds how long Wait may go on after a child has exited.
const childWaitDelay = 5 * time.Second

// clearedEnvPrefixes are the variable families a child must not inherit: every QOMPACK_ variable
// is a configuration input (and this package's whole subject is configuration inputs), and every
// CLAUDE_ variable is a host input that would point at the developer's real installed plugin.
var clearedEnvPrefixes = []string{"QOMPACK_", "CLAUDE_"}

// run executes bin with args, feeding it stdin, and returns stdout, stderr and the exit code. A
// failure to START is a test failure; a non-zero exit is returned, never asserted here.
func run(t *testing.T, bin, dir string, args []string, stdin []byte, env map[string]string) (stdout, stderr []byte, code int) {
	t.Helper()

	stdio, cleanup := newChildStdio(t, stdin)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), runBound)
	defer cancel()

	started := time.Now()
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // G204: a binary this package assembled, with arguments it chose
	cmd.Dir = dir
	cmd.Env = childEnv(env)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdio.in, stdio.out, stdio.err
	cmd.WaitDelay = childWaitDelay

	err := cmd.Run()
	elapsed := time.Since(started)
	stdout, stderr = stdio.read(t)

	var exitErr *exec.ExitError
	switch {
	case err == nil:
		code = 0
	case ctx.Err() != nil:
		t.Fatalf("release: %s %v did not finish within %s (elapsed %s); stderr:\n%s",
			filepath.Base(bin), args, runBound, elapsed.Round(time.Millisecond), stderr)
	case errors.Is(err, exec.ErrWaitDelay):
		code = 0
		t.Logf("release: %s %v exited 0 but its I/O was still open after %s (WaitDelay fired) — "+
			"a process it spawned inherited a standard handle; elapsed %s",
			filepath.Base(bin), args, childWaitDelay, elapsed.Round(time.Millisecond))
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		t.Fatalf("release: could not run %s %v: %v\nstderr:\n%s", bin, args, err, stderr)
	}
	return stdout, stderr, code
}

// childStdio is one invocation's three standard streams, as real files in a disposable directory.
type childStdio struct {
	dir              string
	in, out, err     *os.File
	outPath, errPath string
}

func openChildStdio(stdin []byte, tmpPrefix string) (childStdio, func(), error) {
	dir, err := os.MkdirTemp("", tmpPrefix)
	if err != nil {
		return childStdio{}, nil, fmt.Errorf("creating a stdio directory: %w", err)
	}
	c := childStdio{dir: dir, outPath: filepath.Join(dir, "stdout"), errPath: filepath.Join(dir, "stderr")}
	inPath := filepath.Join(dir, "stdin")
	if err := os.WriteFile(inPath, stdin, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return childStdio{}, nil, fmt.Errorf("writing the child's stdin: %w", err)
	}
	if c.in, err = os.Open(inPath); err != nil {
		_ = os.RemoveAll(dir)
		return childStdio{}, nil, fmt.Errorf("opening the child's stdin: %w", err)
	}
	if c.out, err = os.Create(c.outPath); err != nil {
		_ = c.in.Close()
		_ = os.RemoveAll(dir)
		return childStdio{}, nil, fmt.Errorf("creating the child's stdout: %w", err)
	}
	if c.err, err = os.Create(c.errPath); err != nil {
		_ = c.in.Close()
		_ = c.out.Close()
		_ = os.RemoveAll(dir)
		return childStdio{}, nil, fmt.Errorf("creating the child's stderr: %w", err)
	}
	return c, func() {
		for _, f := range []*os.File{c.in, c.out, c.err} {
			_ = f.Close()
		}
		_ = os.RemoveAll(paths.Long(dir))
	}, nil
}

func newChildStdio(t *testing.T, stdin []byte) (childStdio, func()) {
	t.Helper()
	c, cleanup, err := openChildStdio(stdin, "qompack-release-stdio-")
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	return c, cleanup
}

func (c childStdio) readBytes() (stdout, stderr []byte) {
	_ = c.out.Close()
	_ = c.err.Close()
	stdout, _ = os.ReadFile(c.outPath)
	stderr, _ = os.ReadFile(c.errPath)
	return stdout, stderr
}

func (c childStdio) read(t *testing.T) (stdout, stderr []byte) {
	t.Helper()
	return c.readBytes()
}

// childEnv is the process environment with clearedEnvPrefixes removed and env layered on top. The
// key match is case-insensitive because Windows environment variable names are.
func childEnv(env map[string]string) []string {
	out := make([]string, 0, len(os.Environ())+len(env))
	for _, kv := range os.Environ() {
		key, _, ok := strings.Cut(kv, "=")
		if ok && hasAnyPrefixFold(key, clearedEnvPrefixes) {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// hasAnyPrefixFold reports whether s starts with one of prefixes, ignoring case.
func hasAnyPrefixFold(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if len(s) >= len(p) && strings.EqualFold(s[:len(p)], p) {
			return true
		}
	}
	return false
}

// hookSubcommands is §7.3's six hook entry points paired with the host event each answers.
var hookSubcommands = []struct {
	event string
	argv  []string
}{
	{"PostToolUse", []string{"observe", "tool"}},
	{"UserPromptSubmit", []string{"observe", "prompt"}},
	{"Stop", []string{"observe", "stop"}},
	{"SessionStart", []string{"session-start"}},
	{"PreCompact", []string{"checkpoint"}},
	{"SessionEnd", []string{"flush"}},
}

// releaseSessionID is the session every case presents to a hook.
const releaseSessionID = core.SessionID("sess-release-0001")

// hookPayload renders the representative payload for one hook, with a NATIVE absolute cwd: a
// POSIX-flavoured path on Windows resolves to a directory that is not there, and the hook then
// answers correctly while observing nothing.
func hookPayload(t *testing.T, event, cwd, toolUseID, source string) []byte {
	t.Helper()
	if !filepath.IsAbs(cwd) {
		t.Fatalf("release: hook payload cwd %q must be a native absolute path", cwd)
	}
	e := hookio.Event{
		HookEventName:  event,
		SessionID:      releaseSessionID,
		TranscriptPath: filepath.Join(cwd, "transcript.jsonl"),
		CWD:            cwd,
	}
	switch event {
	case "PostToolUse":
		e.ToolName = "Read"
		e.ToolUseID = core.ToolUseID(toolUseID)
		e.ToolInput = json.RawMessage(`{"file_path":"src/auth.ts"}`)
		e.ToolResponse = json.RawMessage(`{"content":"export const authMiddleware = 1;"}`)
	case "UserPromptSubmit":
		e.Prompt = "why does the auth middleware reject an expired token twice?"
	case "SessionStart":
		e.Source = source
	case "PreCompact":
		e.Trigger = "auto"
	case "Stop", "SubagentStop":
		e.StopHookActive = true
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("release: marshalling a %s payload: %v", event, err)
	}
	return b
}

// emptyOrParseableJSON reports whether stdout is something a host could safely consume: nothing at
// all, or one JSON document. It is the half of the hook contract the exit code does not cover.
func emptyOrParseableJSON(stdout []byte) bool {
	trimmed := bytes.TrimSpace(stdout)
	return len(trimmed) == 0 || json.Valid(trimmed)
}

// additionalContext returns the SessionStart injection a hook answered with, or "".
func additionalContext(stdout []byte) string {
	var out hookio.Output
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &out); err != nil {
		return ""
	}
	if out.HookSpecificOutput == nil {
		return ""
	}
	return out.HookSpecificOutput.AdditionalContext
}

// ---------------------------------------------------------------------------
// Project fixtures and the daemon
// ---------------------------------------------------------------------------

// project is one disposable project root and the environment a spawned qompack process needs to
// stay inside it.
type project struct {
	Root string
	Home string
	Env  map[string]string
}

// newProject creates a project under a disposable base. os.MkdirTemp rather than t.TempDir for
// test/canary's reason: t.TempDir fails the test when its cleanup cannot remove the tree, and on
// Windows a daemon that has not finished unwinding makes that a certainty rather than a risk.
//
// The `.git` marker stops paths.Resolve's upward walk at this root rather than letting it escape
// into whatever encloses the OS temp directory.
func newProject(t *testing.T) project {
	t.Helper()
	base, err := os.MkdirTemp("", "qompack-release-")
	if err != nil {
		t.Fatalf("release: creating a temporary directory: %v", err)
	}
	root := filepath.Join(base, "project")
	home := filepath.Join(base, "home")
	for _, d := range []string{filepath.Join(root, ".git"), home} {
		if err := os.MkdirAll(paths.Long(d), 0o700); err != nil {
			t.Fatalf("release: creating %s: %v", d, err)
		}
	}
	p := project{Root: root, Home: home, Env: map[string]string{
		"QOMPACK_PROJECT_ROOT": root, "HOME": home, "USERPROFILE": home,
	}}
	t.Cleanup(func() {
		shutdownIfReachable(t, root)
		_ = os.RemoveAll(paths.Long(base))
	})
	return p
}

// envWith returns the project environment plus one switch.
func (p project) envWith(key, value string) map[string]string {
	env := make(map[string]string, len(p.Env)+1)
	for k, v := range p.Env {
		env[k] = v
	}
	if key != "" {
		env[key] = value
	}
	return env
}

const (
	probeTimeout     = 250 * time.Millisecond
	roundTripDL      = 5 * time.Second
	daemonPollTick   = 100 * time.Millisecond
	daemonDownBound  = 90 * time.Second
	indexWaitBound   = 90 * time.Second
	daemonGoneBound  = 90 * time.Second
	toolUseIndexFile = "tool_use.jsonl"
)

// waitForToolUseIndex waits until the daemon has written the tool_use index, which is how this
// package knows a seeded observation reached the store rather than merely the spool.
func waitForToolUseIndex(t *testing.T, root string) bool {
	t.Helper()
	p := filepath.Join(paths.Of(root).Index, toolUseIndexFile)
	return waitUntil(indexWaitBound, func() bool {
		b, err := os.ReadFile(paths.Long(p))
		return err == nil && len(bytes.TrimSpace(b)) > 0
	})
}

// waitUntil polls cond on a ticker until it holds or the bound expires. A ticker rather than
// time.Sleep, per §6.1's wall-clock-sleep ban (devtool lint's sleepcheck sub-check).
func waitUntil(bound time.Duration, cond func() bool) bool {
	if cond() {
		return true
	}
	ticker := time.NewTicker(daemonPollTick)
	defer ticker.Stop()
	deadline := time.NewTimer(bound)
	defer deadline.Stop()
	for {
		select {
		case <-ticker.C:
			if cond() {
				return true
			}
		case <-deadline.C:
			return false
		}
	}
}

// lockPath is the daemon lock file this package asks about, named once so every message agrees.
func lockPath(root string) string { return daemon.LockPath(root) }

// lockFileExists reports whether root's daemon lock file is present at all, which is the question
// the daemon-disabled case asks: not "is a daemon reachable" but "was one ever started".
func lockFileExists(root string) bool {
	_, err := os.Stat(paths.Long(lockPath(root)))
	return err == nil
}

// shutdownIfReachable sends admin.shutdown until the daemon is gone.
//
// What "gone" means is testutil.ShutdownDaemonUntilGone's one definition, shared with every other
// shutdown helper under test/: no live process holds the lock, every process seen holding it during
// the call has exited, and no holder went unidentified. This helper keeps only what is this
// package's own: a daemon that is still coming up holds the lock while answering no dial at all, so
// a live lock holder counts as something to shut down even when nothing answers.
func shutdownIfReachable(t *testing.T, root string) {
	t.Helper()
	addr, err := ipc.Resolve(root)
	if err != nil {
		return
	}
	if !ipc.Probe(addr, probeTimeout) {
		if _, held := testutil.DaemonHoldingLock(root); !held {
			return
		}
	}
	out := testutil.ShutdownDaemonUntilGone(root, addr, testutil.ShutdownWait{
		Tick: daemonPollTick, Bound: daemonDownBound, RoundTrip: roundTripDL,
	})
	if !out.Gone {
		t.Logf("release: %s", out.Describe(daemon.LockPath(root), daemonDownBound))
	}
}

// stopDaemonAndWait shuts the project's daemon down and waits for the lock file itself to go away,
// so the NEXT hook starts a daemon that reads the case's switch.
//
// This is the load-bearing step of every case that flips a switch on a project the control phase
// already seeded. A running daemon loaded its configuration at startup: an environment variable in
// a later hook process changes the hook, not the resident daemon, and a case that skipped this
// would be measuring a daemon still running under the defaults.
func stopDaemonAndWait(t *testing.T, root string) bool {
	t.Helper()
	shutdownIfReachable(t, root)
	return waitUntil(daemonGoneBound, func() bool { return !lockFileExists(root) })
}

// storeSurfaces lists the files under .qompack/objects and .qompack/index, which together are
// "was anything recorded": objects hold the content, the index holds the references to it.
func storeSurfaces(root string) []string {
	l := paths.Of(root)
	var out []string
	for _, dir := range []string{l.Objects, l.Index} {
		_ = filepath.WalkDir(paths.Long(dir), func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil //nolint:nilerr // a vanished entry is an observation, not a failure
			}
			info, statErr := d.Info()
			if statErr != nil || info.Size() == 0 {
				return nil
			}
			rel, relErr := filepath.Rel(paths.Long(root), p)
			if relErr != nil {
				return nil
			}
			out = append(out, filepath.ToSlash(rel))
			return nil
		})
	}
	sort.Strings(out)
	return out
}

// jsonDoc decodes a command's JSON stdout.
func jsonDoc(t *testing.T, what string, stdout []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &doc); err != nil {
		t.Fatalf("release: %s did not write one JSON document: %v\nstdout:\n%s", what, err, stdout)
	}
	return doc
}

// getPath walks a dotted path through a decoded JSON document.
func getPath(doc map[string]any, dotted string) (any, bool) {
	cur := any(doc)
	for _, seg := range strings.Split(dotted, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}
