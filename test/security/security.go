// Package security is SP-17's trust-and-privacy matrix: the tests that decide whether the thing
// this repository actually ships keeps a secret, refuses an address it is not allowed to resolve,
// stays bounded against hostile bytes, and never runs anything it read back out of its own archive.
//
// # What makes this package different from the suites beside it
//
// internal/redact proves its rules fire. internal/mcp proves its handlers authorize. test/
// integration proves a secret does not survive into `objects/`. None of them drives the ARTIFACT a
// user installs, and none of them looks at the other seventeen places a byte can come to rest. This
// package assembles the host target's plugin bundle once per test binary, through the very task
// Task 1 committed (`go run ./tools/devtool bundle --target <os>/<arch> --out <dir>`), drives that
// bundle's own `bin/qompack[.exe]` for every hook and every MCP session, and then sweeps every file
// the product wrote — objects, indexes, records, checkpoints, pins, spool, state, metrics and logs
// including LOUD.log, a real backup tree, a real eval export, and the retrieval previews the MCP
// server puts on the wire.
//
// # Outcomes, and why a skip is not a pass
//
// Each case writes one evidence record naming the target, the bundle it drove and what happened.
// The outcome vocabulary is three-valued: `verified` (the case ran and the product did what it
// promised), `failed` (the case ran and it did not — a RETURNED finding, recorded with its owning
// package and never fixed from here), and `skipped` (the case did not run, and the reason says what
// was missing). A row that says nothing is a row nobody may cite. That is test/canary's rule
// (SP-19 M0-G2) and test/platform's (SP-17 Task 2), applied to the security axis.
//
// A `failed` record does not automatically fail its Go test, and the split is deliberate. Where the
// finding is a POLICY divergence — a refusal that arrives as a tool error rather than as the denied
// envelope, a configured bound that is refused late rather than clamped early — the record is the
// deliverable and the case goes on. Where the finding is a LEAK — a credential on a durable
// surface, content served for an address that was refused, a sentinel file that proves something
// executed archived text — the Go test fails too, because §13 invariant 7 and §7.1 are not
// advisory. Each site says which it is and why.
//
// # Ownership
//
// Role C owns test/security. Defects it finds are RETURNED: store and GC changes belong to
// internal/store, retrieval envelopes to internal/mcp, capture admission to internal/cli and
// internal/daemon, the export's rule set to internal/eval. Nothing in this package edits any of
// them, and nothing here proposes a fix; the audit prose this package's evidence supports is
// written by the coordinator.
//
// security is a composition root (00-ARCHITECTURE.md §3.2): it imports the tree and nothing may
// import it. It has to be one for the same reason test/e2e, test/canary and test/platform are — it
// reaches store, mcp, eval, config, sketch, daemon, ipc, paths, hookio, core and testutil directly
// and cli through the binary it spawns, and no internal package's allow-set permits that.
package security

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
)

// ---------------------------------------------------------------------------
// Evidence records
// ---------------------------------------------------------------------------

// Outcome is what one security case established.
type Outcome string

// The three outcomes.
const (
	// OutcomeVerified: the case ran on this host and the product honoured its contract.
	OutcomeVerified Outcome = "verified"
	// OutcomeFailed: the case ran and it did not. Reason names the owner of the fix.
	OutcomeFailed Outcome = "failed"
	// OutcomeSkipped: the case did not run here. Reason says what was missing.
	OutcomeSkipped Outcome = "skipped"
)

// Capability is which of this package's four concerns a record belongs to. It is the axis the
// evidence table is grouped by, so it is a closed set rather than free prose.
type Capability string

// The capability axes.
const (
	// CapArchiveTrust: the archive may not be walked into, and its text is never an instruction.
	CapArchiveTrust Capability = "archive_trust"
	// CapPrivacy: no secret reaches a durable surface.
	CapPrivacy Capability = "privacy"
	// CapBounds: decode and size bounds hold against hostile input.
	CapBounds Capability = "bounds"
	// CapPosture: no network, no telemetry, denial before pass-through.
	CapPosture Capability = "posture"
	// CapRetention: diagnostic evidence survives collection.
	CapRetention Capability = "retention"
)

// Scope says what the record is evidence ABOUT: the assembled bundle a user would install, or the
// repository's own source graph. A graph fact and an artifact fact are not interchangeable, and
// collapsing them is how "we checked" comes to mean less than it sounds like.
type Scope string

// The two scopes.
const (
	// ScopeInstalledBundle: the case drove the binary out of an assembled plugin bundle.
	ScopeInstalledBundle Scope = "installed_bundle"
	// ScopeRepository: the case asked a question of this checkout rather than of the artifact.
	ScopeRepository Scope = "repository"
)

// TargetInfo is the platform a record is attributed to.
type TargetInfo struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
	Go   string `json:"go"`
}

// BundleInfo identifies the artifact the case drove. The binary's digest is what makes a record
// re-checkable: two records naming the same version but different digests came from two builds.
type BundleInfo struct {
	Version      string `json:"version"`
	BinarySHA256 string `json:"binary_sha256"`
}

// Record is one security case's retained artifact.
type Record struct {
	Name       string     `json:"name"`
	Capability Capability `json:"capability"`
	Scope      Scope      `json:"scope"`
	Target     TargetInfo `json:"target"`
	Bundle     BundleInfo `json:"bundle"`
	// Outcome is required; writeRecord refuses a record that does not carry one.
	Outcome Outcome `json:"outcome"`
	// Reason is prose and it is the point: a skipped or failed case that does not say why is
	// indistinguishable from one nobody ran.
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
	// Artifact is a supporting file this record points at, when the case produced one.
	Artifact string `json:"artifact,omitempty"`
}

// artifactsEnvKey names the directory records are collected into. Unset (the ordinary `go test`
// case) means the test's own temp directory, so a plain run leaves nothing behind.
const artifactsEnvKey = "QOMPACK_SECURITY_ARTIFACTS"

// artifactIndexName is the manifest of the records ONE run produced, written by TestMain.
//
// It exists because a record file is not self-dating: when the artifact directory points at a
// committed evidence directory and a case fatals before writing, the previous run's file survives
// and presents a stale `verified` row that nothing distinguishes from a fresh one. Two mechanisms
// answer that together — the directory's records are pruned once at the start of a collecting run,
// and this manifest states afterwards exactly which names that run produced.
const artifactIndexName = "INDEX.json"

var (
	// collectOnce guards the single prune-and-create of a collecting run's artifact directory.
	collectOnce sync.Once
	collectDir  string
	collectErr  error

	// tempArtifactMu guards tempArtifactDirs, which caches one temp directory per test so a test
	// writing six records does not scatter them across six directories.
	tempArtifactMu   sync.Mutex
	tempArtifactDirs = map[string]string{}

	// writtenMu guards writtenRecords, the names this run wrote, in write order.
	writtenMu      sync.Mutex
	writtenRecords []string
)

// artifactDir returns where records are written: $QOMPACK_SECURITY_ARTIFACTS when it is set (so a
// collecting run can be committed), else one temp directory per test.
func artifactDir(t *testing.T) string {
	t.Helper()
	if os.Getenv(artifactsEnvKey) != "" {
		collectOnce.Do(prepareCollectDir)
		if collectErr != nil {
			t.Fatalf("security: %v", collectErr)
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

// prepareCollectDir creates the collecting directory and removes the JSON left by any earlier run.
//
// The prune is deliberately unconditional over `*.json` in that directory: pruning only the names
// this run is about to write cannot remove a record whose CASE was deleted or renamed, which is
// exactly the stale row that reads as current. The directory is this package's alone, and the
// manifest written at the end says what survived on purpose.
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

// writeArtifactIndex records which names this run produced, for a collecting run only. TestMain
// calls it after the last case, so a case that fatalled before writing its record is visible as an
// absence in a document written afterwards rather than as nothing at all.
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
	}{
		Target:  hostTarget(),
		Bundle:  BundleInfo{Version: hostBundle.Version, BinarySHA256: hostBundle.BinarySHA256},
		Records: names,
	}

	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(paths.Long(filepath.Join(collectDir, artifactIndexName)), append(b, '\n'), 0o600)
}

// hostTarget is the platform every record this run writes is attributed to.
func hostTarget() TargetInfo {
	return TargetInfo{OS: runtime.GOOS, Arch: runtime.GOARCH, Go: runtime.Version()}
}

// newRecord returns a record pre-filled with this host's target and the assembled bundle's
// identity, so no case has to remember to attribute its own evidence. Scope defaults to the
// bundle, because that is what all but one case here drives.
func newRecord(t *testing.T, name string) Record {
	t.Helper()
	return Record{
		Name: name, Scope: ScopeInstalledBundle,
		Target: hostTarget(), Bundle: hostBundleInfo(t),
	}
}

// writeRecord persists rec as JSON under artifactDir and returns the path. The name is used as the
// file name, so it must be a single path segment: a record whose name carried a separator would
// silently write outside the artifact directory.
func writeRecord(t *testing.T, rec Record) string {
	t.Helper()

	if rec.Outcome == "" {
		t.Fatalf("security %s: a record must carry an outcome", rec.Name)
	}
	if rec.Capability == "" {
		t.Fatalf("security %s: a record must name the capability it is evidence about", rec.Name)
	}
	if rec.Name == "" || rec.Name != filepath.Base(rec.Name) || strings.ContainsAny(rec.Name, `/\`) {
		t.Fatalf("security: record name %q must be a single path segment", rec.Name)
	}
	if rec.Name+".json" == artifactIndexName {
		t.Fatalf("security: %q collides with this run's own manifest", rec.Name)
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatalf("security: marshalling the %s record: %v", rec.Name, err)
	}
	p := filepath.Join(artifactDir(t), rec.Name+".json")
	if err := os.WriteFile(paths.Long(p), append(b, '\n'), 0o600); err != nil {
		t.Fatalf("security: writing %s: %v", p, err)
	}
	writtenMu.Lock()
	writtenRecords = append(writtenRecords, rec.Name)
	writtenMu.Unlock()
	t.Logf("security %s: %s — %s\n  detail:   %s\n  artifact: %s",
		rec.Name, rec.Outcome, rec.Reason, rec.Detail, p)
	return p
}

// platformSkipPrefix is the stubskips-permitted prefix for an environment-gated skip
// (tools/devtool/stubskips.go: the reason after it must be non-empty).
const platformSkipPrefix = "platform: "

// skipRecorded writes rec as `skipped` with the given reason, THEN skips.
//
// The order is the contract, exactly as test/canary's own skipRecorded documents it: a case that
// skips without leaving a record is indistinguishable from one that was never written, and the
// evidence table is assembled from the records rather than from the log.
func skipRecorded(t *testing.T, rec Record, reason string) {
	t.Helper()
	rec.Outcome = OutcomeSkipped
	rec.Reason = reason
	writeRecord(t, rec)
	t.Skip(platformSkipPrefix + rec.Name + ": " + reason)
}

// ---------------------------------------------------------------------------
// The assembled bundle under test
// ---------------------------------------------------------------------------

// bundleAssembleBound is how long `devtool bundle` may take. It cross-compiles cmd/qompack and
// hashes the assembled tree; on a cold build cache, on a machine shared with another suite, that is
// minutes rather than seconds.
const bundleAssembleBound = 15 * time.Minute

// bundle is the assembled host-target bundle every case in this package drives.
type bundle struct {
	// Dir is the bundle root — the directory a host would treat as ${CLAUDE_PLUGIN_ROOT}.
	Dir string
	// Bin is Dir/bin/qompack[.exe]: the binary the manifest names, and the one `.mcp.json` launches
	// in exec form (`args` present) as `<bundle>/bin/qompack mcp`, with no shell in between.
	Bin string
	// Version and BinarySHA256 come from the assembled BUNDLE.json and the binary itself.
	Version      string
	BinarySHA256 string
}

var (
	bundleOnce sync.Once
	hostBundle bundle
	bundleBase string
	bundleErr  error
)

// bundleIdentity mirrors the fields of BUNDLE.json this package reads; every other field the
// document carries is asserted by tools/devtool's own tests.
type bundleIdentity struct {
	Version string `json:"version"`
	Target  struct {
		OS   string `json:"os"`
		Arch string `json:"arch"`
	} `json:"target"`
}

// assembledBundle assembles the host target's bundle once per test binary and returns it. Nothing
// any case does mutates the bundle, so once is enough and a second assembly would only cost minutes.
func assembledBundle(t *testing.T) bundle {
	t.Helper()
	bundleOnce.Do(doAssemble)
	if bundleErr != nil {
		t.Fatalf("security: assembling the host bundle: %v", bundleErr)
	}
	return hostBundle
}

// hostBundleInfo is assembledBundle reduced to what a record carries.
func hostBundleInfo(t *testing.T) BundleInfo {
	t.Helper()
	b := assembledBundle(t)
	return BundleInfo{Version: b.Version, BinarySHA256: b.BinarySHA256}
}

// doAssemble is assembledBundle's body: `go run ./tools/devtool bundle --target <host> --out <tmp>`.
//
// The host target is named explicitly rather than left to the default, because the default
// assembles all six and five of them would be cross-compiles this package cannot execute. No
// --version is passed: the version rule is Task 1's, and pinning one here would make these records
// name a version no release ever carried.
func doAssemble() {
	repo, err := moduleRoot()
	if err != nil {
		bundleErr = err
		return
	}

	bundleBase, bundleErr = os.MkdirTemp("", "qompack-security-bundle-")
	if bundleErr != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), bundleAssembleBound)
	defer cancel()

	target := runtime.GOOS + "/" + runtime.GOARCH
	cmd := exec.CommandContext(ctx, "go", "run", "./tools/devtool", "bundle", //nolint:gosec // G204: fixed argv over this package's own temp directory
		"--target", target, "--out", bundleBase)
	cmd.Dir = repo
	// The assembly deliberately inherits the real environment: a `go run` under a temp-directory
	// HOME resolves a different module and build cache and can spend minutes re-downloading what is
	// already on disk (test/canary's doBuild gives the same reason).
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		bundleErr = fmt.Errorf("go run ./tools/devtool bundle --target %s --out %s (in %s): %w\nstdout:\n%s\nstderr:\n%s",
			target, bundleBase, repo, err, stdout.String(), stderr.String())
		return
	}

	dir, err := soleBundleDir(bundleBase)
	if err != nil {
		bundleErr = fmt.Errorf("%w\nstdout:\n%s", err, stdout.String())
		return
	}

	var id bundleIdentity
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

// soleBundleDir returns the one directory `devtool bundle --target <host>` assembled under base.
// Insisting on exactly one keeps a stale directory from an earlier run — or a second target nobody
// asked for — from being picked up as the artifact under test.
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
		resetPermissionsForCleanup(bundleBase)
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

// moduleRoot returns the repository root: the nearest ancestor of the working directory holding a
// go.mod. `go run ./tools/devtool` is only meaningful from there.
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

// runBound is how long one spawned qompack invocation may take. The longest manifest hook timeout
// is 20 s; anything past this is a hang, not a slow hook, and the margin is for a machine running
// another suite at the same time.
const runBound = 120 * time.Second

// childWaitDelay bounds how long Wait may go on after a child has exited, and it is the second half
// of the fix described on run(): if anything still holds one of the child's standard handles when
// the process is gone, Wait gives up after this rather than blocking on a detached daemon's
// lifetime. A few seconds is far longer than any legitimate flush and far shorter than a hang.
const childWaitDelay = 5 * time.Second

// slowChildNotice is the elapsed time past which one invocation is logged with its duration. It is
// diagnostics, never an assertion: the review's 421-second case was invisible in the log because
// nothing recorded how long each step took, and a timing NOTICE is how that stops being invisible
// without introducing a timing claim this package is forbidden to make.
const slowChildNotice = 5 * time.Second

// clearedEnvPrefixes are the variable families a child of this package must not inherit from
// whoever launched `go test`.
//
// Both matter. Every `QOMPACK_` variable is a configuration or state input — an ambient
// `QOMPACK_RUNTIME__…`, `QOMPACK_PROJECT_ROOT`, `QOMPACK_IPC_ADDR` or the §12.3 fault-injection
// switch would
// silently change what this matrix measures, and `QOMPACK_SECURITY_ARTIFACTS` is set for exactly
// the run that collects evidence and must not reach a child at all. Every `CLAUDE_` variable is a
// host input, and an inherited `CLAUDE_PLUGIN_ROOT` would point at the developer's real installed
// plugin rather than at the bundle under test.
//
// Anything a case actually needs it passes in `env`, which is applied after the strip.
var clearedEnvPrefixes = []string{"QOMPACK_", "CLAUDE_"}

// run executes bin with args, feeding it stdin, and returns stdout, stderr and the exit code.
//
// A failure to START the process fails the test. A non-zero exit is RETURNED, never asserted here:
// several cases exist to observe an exit code rather than to demand one.
//
// # Why all three streams are real files
//
// This is the fix for a hang the independent review reproduced and this host did not: a package run
// that sat past a 30-minute timeout and left a daemon alive for over an hour. The mechanism is in
// os/exec rather than in the product. Handing exec.Cmd an io.Reader for Stdin or an io.Writer for
// Stdout makes it create an OS pipe and a copying goroutine, and `Wait` does not return until every
// one of those goroutines finishes — which means until the pipe's OTHER end is closed by every
// process holding it. A hook spawns a detached daemon; a daemon that inherits any of the hook's
// three standard handles keeps that pipe open after the hook itself has exited, and `Wait` then
// blocks for the daemon's whole lifetime. The `exec.CommandContext` deadline does not help: its
// cancel kills a process that has already exited and the copier goes on waiting.
//
// An *os.File is passed to the child as a file descriptor with no pipe and no goroutine, so `Wait`
// returns when the PROCESS exits and nothing else can extend it. WaitDelay below is the second
// belt: it bounds any residual wait and makes the expiry visible instead of silent.
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
		// The deadline is tested BEFORE the two error shapes below, and the order is the whole
		// point. CommandContext kills the child when the context expires, so `Run` returns the
		// dead process's *exec.ExitError — never ctx.Err() — and an errors.As arm placed first
		// swallows every timeout as an ordinary non-zero exit. A child that ran past runBound is
		// reported as the timeout it is.
		t.Fatalf("security: %s %v did not finish within %s (elapsed %s); stderr:\n%s",
			filepath.Base(bin), args, runBound, elapsed.Round(time.Millisecond), stderr)
	case errors.Is(err, exec.ErrWaitDelay):
		// The process exited cleanly and something still held its I/O open past the delay. With
		// real files that should be unreachable, so it is recorded rather than swallowed: it is the
		// signature of the hang this helper was rewritten to stop.
		code = 0
		t.Logf("security: %s %v exited 0 but its I/O was still open after %s (WaitDelay fired) — "+
			"a process it spawned inherited a standard handle; elapsed %s",
			filepath.Base(bin), args, childWaitDelay, elapsed.Round(time.Millisecond))
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		t.Fatalf("security: could not run %s %v: %v\nstderr:\n%s", bin, args, err, stderr)
	}
	if elapsed > slowChildNotice {
		t.Logf("security: %s %v took %s", filepath.Base(bin), args, elapsed.Round(time.Millisecond))
	}
	return stdout, stderr, code
}

// childStdio is one invocation's three standard streams, as real files in a disposable directory.
type childStdio struct {
	dir              string
	in, out, err     *os.File
	outPath, errPath string
}

// newChildStdio materializes stdin and opens the two output files. The enclosing directory is
// os.MkdirTemp rather than t.TempDir for this package's usual reason: t.TempDir FAILS the test when
// its cleanup cannot remove the tree, and a detached daemon that inherited one of these handles is
// exactly the case that would make removal fail — turning the diagnostic into the failure.
func newChildStdio(t *testing.T, stdin []byte) (childStdio, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "qompack-security-stdio-")
	if err != nil {
		t.Fatalf("security: creating a stdio directory: %v", err)
	}
	c := childStdio{dir: dir, outPath: filepath.Join(dir, "stdout"), errPath: filepath.Join(dir, "stderr")}

	inPath := filepath.Join(dir, "stdin")
	if err := os.WriteFile(inPath, stdin, 0o600); err != nil {
		t.Fatalf("security: writing the child's stdin: %v", err)
	}
	if c.in, err = os.Open(inPath); err != nil {
		t.Fatalf("security: opening the child's stdin: %v", err)
	}
	if c.out, err = os.Create(c.outPath); err != nil {
		t.Fatalf("security: creating the child's stdout: %v", err)
	}
	if c.err, err = os.Create(c.errPath); err != nil {
		t.Fatalf("security: creating the child's stderr: %v", err)
	}

	return c, func() {
		for _, f := range []*os.File{c.in, c.out, c.err} {
			_ = f.Close()
		}
		_ = os.RemoveAll(paths.Long(dir))
	}
}

// read closes the two output handles and returns what the child wrote.
func (c childStdio) read(t *testing.T) (stdout, stderr []byte) {
	t.Helper()
	_ = c.out.Close()
	_ = c.err.Close()
	stdout, _ = os.ReadFile(c.outPath)
	stderr, _ = os.ReadFile(c.errPath)
	return stdout, stderr
}

// childEnv is the process environment with clearedEnvPrefixes removed and env layered on top.
//
// The match on the KEY is case-insensitive because Windows environment variable names are: an
// ambient `claude_plugin_root` is the same variable to the child as `CLAUDE_PLUGIN_ROOT`, and a
// case-sensitive strip would leave it in place on the one platform where the spelling can differ
// (internal/daemon/spawn.go's buildSpawnEnv makes the same point about the fault-injection
// switch it strips).
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

// runHook drives one hook subcommand and asserts the half of the hook contract that holds no matter
// what the payload was: exit 0 (§13 invariant 6) and stdout a host could consume.
func runHook(t *testing.T, bin string, p project, argv []string, payload []byte) {
	t.Helper()
	stdout, stderr, code := run(t, bin, p.Root, argv, payload, p.Env)
	if code != 0 {
		t.Fatalf("security: %v must exit 0, got %d\nstdout:\n%s\nstderr:\n%s", argv, code, stdout, stderr)
	}
	if !emptyOrParseableJSON(stdout) {
		t.Fatalf("security: %v wrote stdout a host cannot parse:\n%s", argv, stdout)
	}
}

// emptyOrParseableJSON reports whether stdout is something a host could safely consume: either
// nothing at all, or one JSON document. It is the half of the hook contract the exit code does not
// cover — a hook that exits 0 while writing garbage to stdout breaks the host just as thoroughly.
func emptyOrParseableJSON(stdout []byte) bool {
	trimmed := bytes.TrimSpace(stdout)
	if len(trimmed) == 0 {
		return true
	}
	return json.Valid(trimmed)
}

// ---------------------------------------------------------------------------
// Hook payloads
// ---------------------------------------------------------------------------

// The payload builders marshal through a raw map rather than hookio.Event, because several fields a
// host sends (tool_input, tool_response) are free-form JSON that Event's typed shape would flatten.
// `cwd` is always a NATIVE absolute path: a POSIX-flavoured cwd on Windows resolves to a directory
// that is not there, and the hook then answers correctly while observing nothing — a green test
// that asserted nothing (test/e2e/hooks_test.go makes the same point).

func marshalPayload(t *testing.T, doc map[string]any) []byte {
	t.Helper()
	root, _ := doc["cwd"].(string)
	if !filepath.IsAbs(root) {
		t.Fatalf("security: a hook payload cwd %q must be a native absolute path", root)
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("security: marshalling a hook payload: %v", err)
	}
	return b
}

func sessionStartPayload(t *testing.T, root string, sess core.SessionID) []byte {
	t.Helper()
	return marshalPayload(t, map[string]any{
		"hook_event_name": "SessionStart", "session_id": string(sess), "cwd": root, "source": "startup",
	})
}

func readToolPayload(t *testing.T, root string, sess core.SessionID, id, path, content string) []byte {
	t.Helper()
	return marshalPayload(t, map[string]any{
		"hook_event_name": "PostToolUse", "session_id": string(sess), "cwd": root,
		"tool_name": "Read", "tool_use_id": id,
		"tool_input":    map[string]string{"file_path": path},
		"tool_response": map[string]string{"content": content},
	})
}

func bashToolPayload(t *testing.T, root string, sess core.SessionID, id, command, out string) []byte {
	t.Helper()
	return marshalPayload(t, map[string]any{
		"hook_event_name": "PostToolUse", "session_id": string(sess), "cwd": root,
		"tool_name": "Bash", "tool_use_id": id,
		"tool_input":    map[string]string{"command": command},
		"tool_response": map[string]any{"exit_code": 0, "stdout": out},
	})
}

func promptPayload(t *testing.T, root string, sess core.SessionID, prompt string) []byte {
	t.Helper()
	return marshalPayload(t, map[string]any{
		"hook_event_name": "UserPromptSubmit", "session_id": string(sess), "cwd": root, "prompt": prompt,
	})
}

func preCompactPayload(t *testing.T, root string, sess core.SessionID) []byte {
	t.Helper()
	return marshalPayload(t, map[string]any{
		"hook_event_name": "PreCompact", "session_id": string(sess), "cwd": root, "trigger": "auto",
	})
}

func sessionEndPayload(t *testing.T, root string, sess core.SessionID) []byte {
	t.Helper()
	return marshalPayload(t, map[string]any{
		"hook_event_name": "SessionEnd", "session_id": string(sess), "cwd": root,
	})
}

// oversizeToolPayload builds a PostToolUse delivery past the hard capture cap, with `marker` planted
// at hookPrefixProbeOffset bytes into the tool_response so that its survival anywhere is a
// measurement of the retained prefix rather than of whether anything was stored at all.
func oversizeToolPayload(t *testing.T, root string, sess core.SessionID, id, marker string) []byte {
	t.Helper()
	var body strings.Builder
	body.Grow(hookOversizeBytes + len(marker) + 64)
	for body.Len() < hookPrefixProbeOffset {
		body.WriteString("filler line that carries nothing anybody needs to keep\n")
	}
	body.WriteString(marker)
	body.WriteString("\n")
	for body.Len() < hookOversizeBytes {
		body.WriteString("more filler after the marker, to carry the delivery past the hard cap\n")
	}
	return readToolPayload(t, root, sess, id, "logs/huge.txt", body.String())
}

// ---------------------------------------------------------------------------
// Project fixtures
// ---------------------------------------------------------------------------

// project is one disposable project root and the environment a spawned qompack process needs to
// stay inside it.
type project struct {
	Root string
	Home string
	Env  map[string]string
}

// tempBase returns a disposable enclosing directory with the permission-resetting cleanup registered
// BEFORE anything is created inside it, so the reset always runs first.
//
// It is os.MkdirTemp rather than t.TempDir for test/canary's reason: t.TempDir fails the test if its
// cleanup cannot remove the tree, and on Windows a daemon that has not finished unwinding makes that
// a certainty rather than a risk. Removal here is best-effort.
func tempBase(t *testing.T) string {
	t.Helper()
	base, err := os.MkdirTemp("", "qompack-security-")
	if err != nil {
		t.Fatalf("security: creating a temporary directory: %v", err)
	}
	t.Cleanup(func() {
		resetPermissionsForCleanup(base)
		_ = os.RemoveAll(paths.Long(base))
	})
	return base
}

// newProjectAt creates a project rooted at exactly the path the caller names, with a sibling HOME.
//
// The `.git` marker stops paths.Resolve's upward walk at this root rather than letting it escape
// into whatever encloses the OS temp directory.
func newProjectAt(t *testing.T, base, rootName string) project {
	t.Helper()

	root := filepath.Join(base, rootName)
	home := filepath.Join(base, "home")
	for _, d := range []string{root, home, filepath.Join(root, ".git")} {
		if err := os.MkdirAll(paths.Long(d), 0o700); err != nil {
			t.Fatalf("security: creating %s: %v", d, err)
		}
	}

	return project{
		Root: root,
		Home: home,
		Env: map[string]string{
			"QOMPACK_PROJECT_ROOT": root,
			"HOME":                 home,
			"USERPROFILE":          home,
		},
	}
}

// newProject is newProjectAt over a fresh temp base, for a case that does not need a sibling
// directory outside the root.
func newProject(t *testing.T, rootName string) project {
	t.Helper()
	return newProjectAt(t, tempBase(t), rootName)
}

// writeProjectFile creates one file inside the project, creating its directories.
func writeProjectFile(t *testing.T, p project, rel, content string) {
	t.Helper()
	full := filepath.Join(p.Root, filepath.FromSlash(rel))
	if err := os.MkdirAll(paths.Long(filepath.Dir(full)), 0o700); err != nil {
		t.Fatalf("security: creating %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(paths.Long(full), []byte(content), 0o600); err != nil {
		t.Fatalf("security: writing %s: %v", full, err)
	}
}

// writeProjectConfig writes <root>/.qompack/config.json, the project configuration layer.
func writeProjectConfig(t *testing.T, p project, doc map[string]any) {
	t.Helper()
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("security: marshalling the project configuration: %v", err)
	}
	dot := paths.Of(p.Root).Dot
	if err := os.MkdirAll(paths.Long(dot), 0o700); err != nil {
		t.Fatalf("security: creating %s: %v", dot, err)
	}
	if err := os.WriteFile(paths.Long(filepath.Join(dot, "config.json")), append(b, '\n'), 0o600); err != nil {
		t.Fatalf("security: writing the project configuration: %v", err)
	}
}

// resetPermissionsForCleanup best-effort undoes anything a case did to dir's permissions, so the
// enclosing RemoveAll is never blocked. It is registered before the directory is populated, so it
// always runs first (cloned from test/e2e's resetPermissionsForCleanup, which nothing may import).
func resetPermissionsForCleanup(dir string) {
	_ = filepath.Walk(paths.Long(dir), func(p string, info os.FileInfo, err error) error {
		if err == nil {
			_ = os.Chmod(p, 0o700)
		}
		return nil
	})
	if runtime.GOOS == "windows" {
		_ = exec.Command("icacls", dir, "/reset", "/t", "/c").Run() //nolint:gosec // G204: fixed subcommand over this package's own temp directory
	}
}

// ---------------------------------------------------------------------------
// Links that escape the project root
// ---------------------------------------------------------------------------

// linkMechanism reports how this host can build a link that escapes the project root, and why it
// cannot when it cannot.
//
// os.Symlink is tried first on every platform, because a symlink is the shape the threat model
// names. On Windows it needs SeCreateSymbolicLinkPrivilege (or Developer Mode) and ordinarily
// fails; for a DIRECTORY there is a second mechanism that needs no privilege at all — an NTFS
// junction — and it is a real reparse point that a read walks through exactly as it would a
// symlink. Using it where it works is what keeps the headline case from being skipped on the very
// host this matrix is being recorded on; there is no junction for a FILE, so that shape skips.
func linkMechanism(dir bool) (mechanism, skipReason string) {
	if symlinkWorks(dir) {
		return "os.Symlink", ""
	}
	if runtime.GOOS == "windows" && dir {
		if junctionWorks() {
			return "an NTFS directory junction (mklink /J)", ""
		}
		return "", "neither os.Symlink nor mklink /J could create a directory link here; " +
			"symlink creation requires SeCreateSymbolicLinkPrivilege or Developer Mode"
	}
	return "", "symlink creation requires SeCreateSymbolicLinkPrivilege or Developer Mode"
}

// symlinkWorks probes, in a throwaway directory, whether this process may create a symlink.
func symlinkWorks(dir bool) bool {
	base, err := os.MkdirTemp("", "qompack-security-symprobe-")
	if err != nil {
		return false
	}
	defer func() { _ = os.RemoveAll(paths.Long(base)) }()

	target := filepath.Join(base, "target")
	if dir {
		if err := os.Mkdir(paths.Long(target), 0o700); err != nil {
			return false
		}
	} else if err := os.WriteFile(paths.Long(target), []byte("probe"), 0o600); err != nil {
		return false
	}
	return os.Symlink(target, filepath.Join(base, "link")) == nil
}

// junctionWorks probes whether `mklink /J` can create a directory junction here.
func junctionWorks() bool {
	base, err := os.MkdirTemp("", "qompack-security-junctionprobe-")
	if err != nil {
		return false
	}
	defer func() { _ = os.RemoveAll(paths.Long(base)) }()

	target := filepath.Join(base, "target")
	if err := os.Mkdir(paths.Long(target), 0o700); err != nil {
		return false
	}
	return makeJunction(filepath.Join(base, "link"), target) == nil
}

// makeLink creates a link at linkPath pointing at target, by whatever mechanism this host supports.
func makeLink(linkPath, target string, dir bool) error {
	if err := os.Symlink(target, linkPath); err == nil {
		return nil
	} else if runtime.GOOS != "windows" || !dir {
		return err
	}
	return makeJunction(linkPath, target)
}

// makeJunction creates an NTFS directory junction. cmd's own `mklink` is the only mechanism the
// standard library does not expose, and it is a builtin rather than an executable, hence `cmd /c`.
func makeJunction(linkPath, target string) error {
	out, err := exec.Command("cmd", "/c", "mklink", "/J", linkPath, target).CombinedOutput() //nolint:gosec // G204: fixed subcommand over this package's own temp directories
	if err != nil {
		return fmt.Errorf("mklink /J %s %s: %w: %s", linkPath, target, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Daemon lifecycle
// ---------------------------------------------------------------------------

const (
	// probeTimeout bounds one reachability dial.
	probeTimeout = 250 * time.Millisecond
	// roundTripDeadline bounds one admin request's connect and ACK.
	roundTripDeadline = 5 * time.Second
	// daemonUpBound is how long a case waits for session-start's daemon to answer a dial. It is
	// longer than daemon.SpawnPollBound on purpose: EnsureRunning has already waited that long, so
	// anything still outstanding is a cold start on a loaded machine.
	daemonUpBound = 60 * time.Second
	// daemonPollTick is the interval every poll here re-asks on. A ticker, not time.Sleep, per
	// §6.1's wall-clock-sleep ban (devtool lint's sleepcheck sub-check).
	daemonPollTick = 100 * time.Millisecond
	// daemonDownBound is how long shutdownIfReachable retries admin.shutdown before saying so.
	daemonDownBound = daemon.StopCleanupBound + 15*time.Second
	// indexBound is how long a case waits for the observer to index a hook's event. The ACK is sent
	// after the WAL append and BEFORE the store write, so the bytes a recall needs land shortly
	// after the hook process has already exited.
	indexBound = 30 * time.Second
)

// waitDaemonUp polls root's resolved address until something answers, and reports whether it did.
func waitDaemonUp(t *testing.T, root string) bool {
	t.Helper()
	return waitDaemonUpFor(t, root, daemonUpBound)
}

// absentDaemonBound is how long a case that EXPECTS no daemon waits before saying so.
//
// It is short on purpose and the asymmetry is deliberate. Waiting the full daemonUpBound to confirm
// an absence costs a minute per case and establishes nothing the first few seconds did not: a
// session-start that refused admission never reaches its daemon bootstrap at all, so there is no
// cold start to be patient about. The two cases that use it are measuring a refusal, not a race.
const absentDaemonBound = 10 * time.Second

// waitDaemonUpFor is waitDaemonUp with the caller's own bound.
func waitDaemonUpFor(t *testing.T, root string, bound time.Duration) bool {
	t.Helper()
	addr, err := ipc.Resolve(root)
	if err != nil {
		t.Fatalf("security: ipc.Resolve(%s): %v", root, err)
	}
	started := time.Now()
	if ipc.Probe(addr, probeTimeout) {
		return true
	}
	ticker := time.NewTicker(daemonPollTick)
	defer ticker.Stop()
	deadline := time.NewTimer(bound)
	defer deadline.Stop()
	for {
		select {
		case <-ticker.C:
			if ipc.Probe(addr, probeTimeout) {
				t.Logf("security: a daemon answered %s after %s", root, time.Since(started).Round(time.Millisecond))
				return true
			}
		case <-deadline.C:
			// Every expiry says what it was waiting for and for how long. The review's run spent
			// hundreds of seconds in waits whose logs said nothing at all, which is what made the
			// cause unattributable; a bound that expires silently is barely better than none.
			pid, held := daemonHoldingLock(root)
			t.Logf("security: no daemon answered %s within %s (lock pid %d, held=%v)",
				root, bound, pid, held)
			return false
		}
	}
}

// requireIndexed waits until index/tool_use.jsonl names id, so a retrieval that follows is asking
// about something that is actually there.
func requireIndexed(t *testing.T, root, id string) {
	t.Helper()
	path := filepath.Join(paths.Of(root).Index, "tool_use.jsonl")

	ticker := time.NewTicker(daemonPollTick)
	defer ticker.Stop()
	deadline := time.NewTimer(indexBound)
	defer deadline.Stop()
	started := time.Now()
	for {
		if b, err := paths.ReadFileShared(path); err == nil && bytes.Contains(b, []byte(id)) {
			if waited := time.Since(started); waited > slowChildNotice {
				t.Logf("security: %s was indexed after %s", id, waited.Round(time.Millisecond))
			}
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			size := int64(-1)
			if fi, statErr := os.Stat(paths.Long(path)); statErr == nil {
				size = fi.Size()
			}
			pid, held := daemonHoldingLock(root)
			t.Fatalf("security: the observer never indexed %s into %s within %s "+
				"(index size %d bytes, daemon lock pid %d held=%v)",
				id, path, indexBound, size, pid, held)
		}
	}
}

// requireFileVersion waits until index/files.jsonl records a version of rel (a project-relative,
// slash-separated path), so a re_read that follows asks about a version that has been captured.
//
// requireIndexed cannot stand in for it. The observer publishes the tool_use record first and
// appends the §8.2 file version afterwards (internal/observer/tooluse.go, step 7), behind the
// publication syncs and the capture-reference link of steps 6a-6b. In that window the record is
// visible and re_read's answer — "no historical version has been captured for this path yet" — is
// the correct one; on the Linux verification host the window measured 1.1-2.5 s, longer than the
// MCP child takes to start, which is how TestSecurity_ReReadAnswersFromTheArchiveNotTheLiveDisk
// failed 5 of 5 there while passing wherever the child happened to start later.
func requireFileVersion(t *testing.T, root, rel string) {
	t.Helper()
	path := filepath.Join(paths.Of(root).Index, "files.jsonl")

	ticker := time.NewTicker(daemonPollTick)
	defer ticker.Stop()
	deadline := time.NewTimer(indexBound)
	defer deadline.Stop()
	started := time.Now()
	for {
		if b, err := paths.ReadFileShared(path); err == nil && fileVersionNames(b, rel) {
			if waited := time.Since(started); waited > slowChildNotice {
				t.Logf("security: a version of %s was recorded after %s", rel, waited.Round(time.Millisecond))
			}
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			pid, held := daemonHoldingLock(root)
			t.Fatalf("security: the observer never recorded a file version of %s into %s within %s "+
				"(daemon lock pid %d held=%v)", rel, path, indexBound, pid, held)
		}
	}
}

// fileVersionNames reports whether any complete line of a files.jsonl image records rel.
func fileVersionNames(b []byte, rel string) bool {
	for _, line := range bytes.Split(b, []byte("\n")) {
		var rec struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(line, &rec) == nil && rec.Path == rel {
			return true
		}
	}
	return false
}

// shutdownIfReachable dials root's resolved address and, if anything answers or a live process still
// holds the lock, sends admin.shutdown until the daemon goes away.
//
// This is test/e2e's e2eShutdownIfReachable reduced to what this package needs, cloned rather than
// imported because test/e2e is a composition root. The two properties worth keeping are the ones its
// own comments were written around: "gone" is the LOCK disappearing rather than the address going
// unreachable, because Stop closes the listener first and then goes on writing under .qompack/ for
// the rest of its unwind; and a daemon that is still COMING UP holds the lock while answering no
// dial at all, so liveness of the lock holder — not reachability — decides whether there is anything
// to wait for.
//
// It never signals a process: shutdown is requested over the daemon's own admin channel, which is
// the only mechanism this package uses to stop anything it did not itself fork.
func shutdownIfReachable(t *testing.T, root string) {
	t.Helper()
	addr, err := ipc.Resolve(root)
	if err != nil {
		return
	}
	if !ipc.Probe(addr, probeTimeout) {
		if _, held := daemonHoldingLock(root); !held {
			return
		}
	}

	sp, _ := ipc.NewSpool(paths.Of(root).Spool)
	c := ipc.NewClientWithOptions(addr, sp, nil, nil, ipc.ClientOptions{
		ProjectRoot:     root,
		ConnectDeadline: roundTripDeadline,
		AckDeadline:     roundTripDeadline,
	})
	defer func() { _ = c.Close() }()

	shutdownPID, _ := daemonHoldingLock(root)

	ticker := time.NewTicker(daemonPollTick)
	defer ticker.Stop()
	timeout := time.NewTimer(daemonDownBound)
	defer timeout.Stop()
	for {
		_, _ = c.Send(context.Background(), ipc.Request{
			Op: ipc.OpAdminShutdown, TS: core.NowMilli(core.SystemClock()), Reply: true,
		}, roundTripDeadline)

		lockPID, held := daemonHoldingLock(root)
		if !held && processSettled(shutdownPID) {
			return
		}
		select {
		case <-ticker.C:
		case <-timeout.C:
			t.Logf("security: a daemon (lock pid %d, shutdown pid %d) still held %s after %s of "+
				"retried admin.shutdown", lockPID, shutdownPID, daemon.LockPath(root), daemonDownBound)
			terminateOwnDaemon(t, root, lockPID)
			return
		}
	}
}

// fixturePrefix is the name every temporary directory this package creates begins with. It is what
// makes "a process this package started" decidable from a path, and nothing is ever signalled
// without it.
const fixturePrefix = "qompack-security-"

// terminateOwnDaemon is the last resort when a daemon will not answer admin.shutdown: kill the
// process the lock names, but ONLY when the project it is serving is one of this package's own
// fixtures.
//
// It exists because of a measured consequence: the independent review's run left a daemon alive in
// a temp project for over seventy minutes after the test binary was killed, still holding a tree the
// harness had given up on. A cleanup that gives up quietly is how that happens.
//
// The guard is the whole of its safety. This machine runs other sessions' suites, and a daemon that
// is not serving a directory named with this package's fixture prefix belongs to one of them: it is
// logged and left strictly alone. A pid alone is never enough to justify a signal.
func terminateOwnDaemon(t *testing.T, root string, pid int) {
	t.Helper()
	if pid <= 0 || pid == os.Getpid() || !testutil.ProcessAlive(pid) {
		return
	}
	if !strings.Contains(filepath.ToSlash(root), fixturePrefix) {
		t.Logf("security: leaving daemon pid %d alone: %s is not one of this package's fixtures",
			pid, root)
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		t.Logf("security: could not address daemon pid %d for %s: %v", pid, root, err)
		return
	}
	if err := proc.Kill(); err != nil {
		t.Logf("security: could not terminate daemon pid %d for %s: %v", pid, root, err)
		return
	}
	t.Logf("security: terminated daemon pid %d, which would not answer admin.shutdown for the "+
		"fixture project %s", pid, root)
}

// processSettled reports whether the pid that held the lock can no longer write inside the tree.
func processSettled(shutdownPID int) bool {
	if shutdownPID == 0 || shutdownPID == os.Getpid() {
		return true
	}
	return !testutil.ProcessAlive(shutdownPID)
}

// daemonHoldingLock reports the pid recorded in root's daemon.lock and whether a live process still
// holds it. The lock is read with paths.ReadFileShared, whose handle carries FILE_SHARE_DELETE, so
// polling it cannot make the daemon's own Release fail on Windows and thereby CAUSE the abandoned
// lock it is checking for.
func daemonHoldingLock(root string) (pid int, held bool) {
	b, err := paths.ReadFileShared(daemon.LockPath(root))
	if err != nil {
		return 0, false
	}
	var info daemon.LockInfo
	if err := json.Unmarshal(b, &info); err != nil {
		return 0, true
	}
	return info.PID, testutil.ProcessAlive(info.PID)
}

// ---------------------------------------------------------------------------
// The packaged MCP server
// ---------------------------------------------------------------------------

const (
	// mcpProtocolVersion is the protocol a host negotiates. It is written out rather than read from
	// the package so these records state the version the launcher was actually driven with.
	mcpProtocolVersion = "2025-06-18"
	// mcpReplyBound bounds one JSON-RPC round trip through the child: the stdio process's own
	// 5 s call deadline plus its full retry budget plus margin for a loaded machine.
	mcpReplyBound = 30 * time.Second
	// mcpExitBound bounds the child's exit after its stdin closes.
	mcpExitBound = 30 * time.Second
	// mcpLineBound is the largest JSON-RPC line the child may write. A tools/list result carrying
	// eight schemas, and a full expand, both exceed bufio's 64 KiB default.
	mcpLineBound = 8 << 20
	// metaUntrustedKey is the `_meta.qompack` key every response carrying archived text must set.
	metaUntrustedKey = "untrusted"
)

// mcpChild is a running `qompack mcp` process with its stdio wired to this test. It is launched the
// way plugin/.mcp.json instructs a host to launch it — the bundled binary, the `mcp` subcommand, no
// shell — because that is the process a user's session actually speaks to.
type mcpChild struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	lines  chan string
	stderr *lockedBuffer
	nextID int
	closed bool
}

// startMCP launches the bundled binary's `mcp` subcommand against p.
func startMCP(t *testing.T, bin string, p project) *mcpChild {
	t.Helper()

	cmd := exec.CommandContext(context.Background(), bin, "mcp") //nolint:gosec // G204: a binary this package assembled
	// A neutral working directory that is not the repository, so a root resolution that fell back
	// to the process cwd could not reach this checkout.
	cmd.Dir = filepath.Dir(bin)
	cmd.Env = childEnv(p.Env)
	// The MCP session is an interactive protocol, so this child genuinely needs pipes and cannot
	// take run()'s file-backed stdio. WaitDelay is therefore the only bound available, and it is
	// required for the same reason: `qompack mcp` lazily spawns a daemon, and a daemon that
	// inherited one of these handles would otherwise hold Wait open for its whole lifetime.
	cmd.WaitDelay = childWaitDelay

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("security: stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("security: stdout pipe: %v", err)
	}
	stderr := &lockedBuffer{}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("security: starting %s mcp: %v", bin, err)
	}

	// stdout is drained continuously rather than read on demand: a child that fills a pipe buffer
	// while the parent is writing to its stdin deadlocks both.
	lines := make(chan string, 64)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64<<10), mcpLineBound)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()

	return &mcpChild{cmd: cmd, stdin: stdin, lines: lines, stderr: stderr}
}

// handshake performs the host's own opening sequence: initialize, then the initialized notification.
func (c *mcpChild) handshake(t *testing.T) {
	t.Helper()
	raw := c.request(t, "initialize", map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "qompack-security", "version": "0"},
	})
	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(raw, &initialized); err != nil {
		t.Fatalf("security: decoding the initialize result: %v", err)
	}
	if initialized.ProtocolVersion != mcpProtocolVersion {
		t.Fatalf("security: the launcher negotiated %q, not the offered %q",
			initialized.ProtocolVersion, mcpProtocolVersion)
	}
	c.notify(t, "notifications/initialized")
}

// callResult is one tools/call answer as it reaches the host.
type callResult struct {
	// Tool is the tool that was called, for failure messages.
	Tool string
	// Text is the single text block a tool result carries, which is the tool's own JSON body.
	Text string
	// IsError marks a TOOL-level error — a refusal the model is meant to read — as distinct from a
	// JSON-RPC protocol error, which fails the test outright.
	IsError bool
	Meta    map[string]map[string]any
}

// call drives one tools/call and returns the envelope WITHOUT asserting anything about it.
//
// Not asserting is the point: several cases here exist to observe whether a refusal arrives as a
// denied body, an unavailable body or a tool error, and a helper that required success would make
// exactly those observations impossible.
func (c *mcpChild) call(t *testing.T, name string, args map[string]any) callResult {
	t.Helper()
	raw := c.request(t, "tools/call", map[string]any{"name": name, "arguments": args})

	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool                      `json:"isError"`
		Meta    map[string]map[string]any `json:"_meta"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("security: decoding the %s result: %v\nraw: %s", name, err, raw)
	}
	out := callResult{Tool: name, IsError: res.IsError, Meta: res.Meta}
	for _, block := range res.Content {
		if block.Type == "text" {
			out.Text += block.Text
		}
	}
	return out
}

// request sends one JSON-RPC request and returns its result, failing on a protocol error.
func (c *mcpChild) request(t *testing.T, method string, params any) json.RawMessage {
	t.Helper()
	c.nextID++
	id := c.nextID
	c.send(t, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})

	timer := time.NewTimer(mcpReplyBound)
	defer timer.Stop()
	select {
	case line, ok := <-c.lines:
		if !ok {
			t.Fatalf("security: the launcher closed stdout before answering %s; stderr:\n%s",
				method, c.stderr.String())
		}
		var w struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      *int            `json:"id"`
			Result  json.RawMessage `json:"result"`
			Error   json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &w); err != nil {
			t.Fatalf("security: every stdout line must be JSON-RPC; got %q", line)
		}
		if len(w.Error) > 0 {
			t.Fatalf("security: %s answered with a protocol error: %s", method, w.Error)
		}
		if w.ID == nil || *w.ID != id {
			t.Fatalf("security: responses must arrive in request order; wanted id=%d, got %q", id, line)
		}
		return w.Result
	case <-timer.C:
		t.Fatalf("security: no response to %s within %s; stderr:\n%s", method, mcpReplyBound, c.stderr.String())
		return nil
	}
}

// notify sends one JSON-RPC notification, which carries no id and must produce no response.
func (c *mcpChild) notify(t *testing.T, method string) {
	t.Helper()
	c.send(t, map[string]any{"jsonrpc": "2.0", "method": method})
}

func (c *mcpChild) send(t *testing.T, doc map[string]any) {
	t.Helper()
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("security: marshalling a JSON-RPC request: %v", err)
	}
	if _, err := io.WriteString(c.stdin, string(b)+"\n"); err != nil {
		t.Fatalf("security: writing to the launcher's stdin: %v; stderr:\n%s", err, c.stderr.String())
	}
}

// finish closes the child's stdin, drains what it writes on the way out, and asserts a clean exit.
// An MCP session ends when the host closes the pipe, and `qompack mcp` exits 0 on that EOF.
func (c *mcpChild) finish(t *testing.T) {
	t.Helper()
	if c.closed {
		return
	}
	c.closed = true
	if err := c.stdin.Close(); err != nil {
		t.Fatalf("security: closing the launcher's stdin: %v", err)
	}

	var trailing []string
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for line := range c.lines {
			trailing = append(trailing, line)
		}
	}()

	waited := make(chan error, 1)
	go func() { waited <- c.cmd.Wait() }()

	timer := time.NewTimer(mcpExitBound)
	defer timer.Stop()
	select {
	case err := <-waited:
		// The drain is bounded too. Wait closing the pipes is what normally ends the scanner, but
		// "normally" is precisely what the review's hang was not, and an unbounded receive here
		// would turn a held pipe into a second silent stall.
		drainTimer := time.NewTimer(mcpExitBound)
		defer drainTimer.Stop()
		select {
		case <-drained:
		case <-drainTimer.C:
			t.Fatalf("security: the mcp server exited but its stdout was still held after %s; "+
				"stderr:\n%s", mcpExitBound, c.stderr.String())
		}
		// ErrWaitDelay means the process itself exited cleanly and something kept its I/O open past
		// the delay. That is a diagnostic about handle inheritance, not a failed session.
		if errors.Is(err, exec.ErrWaitDelay) {
			t.Logf("security: the mcp server exited 0 but its I/O was still open after %s "+
				"(WaitDelay fired)", childWaitDelay)
		} else if err != nil {
			t.Fatalf("security: the mcp server must exit 0 on EOF: %v\nstderr:\n%s", err, c.stderr.String())
		}
	case <-timer.C:
		_ = c.cmd.Process.Kill()
		t.Fatalf("security: the mcp server did not exit within %s of its stdin closing; stderr:\n%s",
			mcpExitBound, c.stderr.String())
	}

	if len(trailing) > 0 {
		t.Fatalf("security: the server wrote unsolicited lines after the last request: %v", trailing)
	}
}

// stop is the cleanup half of finish: it takes away a child this package started and that a failing
// case may have left running. It only ever kills a process THIS function's own startMCP forked.
//
// It takes the *testing.T so that neither bound can expire silently. finish NAMES what it was
// waiting for when a bound fires, and a cleanup that abandons a wait without saying so is how an
// orphaned child becomes invisible — the exact failure mode this package exists to keep visible. It
// logs rather than fails: stop runs from t.Cleanup on a path where something has usually already
// gone wrong, and a second verdict there would displace the first. An expiry logged here means a
// child survived a Kill, which is a diagnostic about the process tree, not about the case.
func (c *mcpChild) stop(t *testing.T) {
	t.Helper()
	if c.closed {
		return
	}
	c.closed = true
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	// Drain BEFORE Wait, the same order finish uses and for the same documented reason: Wait closes
	// the stdout pipe once the child has exited, so calling it while the scanner goroutine is still
	// reading is a use-after-close. The kill above is what makes the drain terminate — the child's
	// stdout reaches EOF, the scanner returns, and the channel closes.
	//
	// Both halves are bounded. This runs from t.Cleanup on a path where something has ALREADY gone
	// wrong, and a cleanup that blocks forever is how one failing case becomes a suite that never
	// finishes — which is exactly what the independent review observed.
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range c.lines { //nolint:revive // draining the scanner's channel so its goroutine can exit
		}
	}()
	select {
	case <-drained:
	case <-time.After(mcpExitBound):
		t.Logf("security: cleanup killed the mcp server and its stdout was still held after %s; "+
			"the drain was abandoned and its goroutine is still reading; stderr:\n%s",
			mcpExitBound, c.stderr.String())
	}

	waited := make(chan error, 1)
	go func() { waited <- c.cmd.Wait() }()
	select {
	case <-waited:
	case <-time.After(mcpExitBound):
		t.Logf("security: cleanup killed the mcp server and it had not been reaped %s later; "+
			"the wait was abandoned, so a child of this test may have outlived it; stderr:\n%s",
			mcpExitBound, c.stderr.String())
	}
}

// lockedBuffer is a mutex-guarded buffer for a child's stderr.
//
// The lock is not defensive noise. Handing exec.Cmd a Stderr that is not an *os.File makes it copy
// the pipe on its own goroutine, and every failure message here reads that buffer WHILE the child is
// still running. A bare bytes.Buffer there is a data race the race detector would find — and, worse,
// one that only appears on the paths that are already failing.
type lockedBuffer struct {
	mu sync.Mutex
	b  []byte
}

func (w *lockedBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.b = append(w.b, p...)
	return len(p), nil
}

func (w *lockedBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.b)
}

// ---------------------------------------------------------------------------
// Reading a retrieval envelope
// ---------------------------------------------------------------------------

// retrievalBody is the union of the shapes the eight tools render. Reading them through one struct
// is what lets describeEnvelope name a shape without knowing which tool produced it.
type retrievalBody struct {
	Found     bool              `json:"found"`
	Denied    bool              `json:"denied"`
	Available *bool             `json:"available"`
	Reason    string            `json:"reason"`
	Content   string            `json:"content"`
	Hits      []json.RawMessage `json:"hits"`
	Count     int               `json:"count"`
	Searched  string            `json:"searched"`
	Withheld  bool              `json:"summaries_withheld"`
}

// decodeBody parses a tool's JSON body, returning false when the tool answered with something that
// is not one (a tool error's text is prose, not a document).
func decodeBody(res callResult) (retrievalBody, bool) {
	var b retrievalBody
	if err := json.Unmarshal([]byte(res.Text), &b); err != nil {
		return retrievalBody{}, false
	}
	return b, true
}

// describeEnvelope names the SHAPE of one answer in a handful of characters, so a record can carry
// the whole matrix on one line and a reader can see at a glance where three tools disagree.
func describeEnvelope(res callResult) string {
	if res.IsError {
		return "tool_error"
	}
	body, ok := decodeBody(res)
	if !ok {
		// The prefix is carried rather than swallowed. A record that said only "non_json" would
		// leave a reader unable to tell an empty content block from a prose answer from a document
		// this struct simply does not model, and those are three different facts about a tool.
		return "non_json(" + snippet(res.Text) + ")"
	}
	switch {
	case body.Denied:
		return fmt.Sprintf("denied(found=%v)", body.Found)
	case body.Available != nil && !*body.Available:
		return "unavailable"
	case len(body.Hits) > 0:
		if body.Withheld {
			return fmt.Sprintf("hits(%d,summaries_withheld)", len(body.Hits))
		}
		return fmt.Sprintf("hits(%d)", len(body.Hits))
	case body.Found && body.Content != "":
		return fmt.Sprintf("content(%d bytes)", len(body.Content))
	case body.Found:
		return "found(no content)"
	case body.Searched != "":
		return "miss"
	default:
		return "empty"
	}
}

// snippet is a bounded, single-line rendering of an answer this package could not decode, for a
// record's shape column. It is bounded because a record is committed evidence and an unbounded
// quotation of a tool's own output has no place in one.
func snippet(s string) string {
	const shown = 72
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "empty text block"
	}
	if len(s) <= shown {
		return s
	}
	return s[:shown] + "…"
}

// carriesArchivedText reports whether this response put stored bytes on the wire, which is what
// obliges it to carry the untrusted marker.
func carriesArchivedText(res callResult) bool {
	body, ok := decodeBody(res)
	if !ok {
		return false
	}
	return body.Content != "" || len(body.Hits) > 0
}

// renderShapes is the per-tool envelope map as one deterministic line.
func renderShapes(shapes map[string]string) string {
	keys := make([]string, 0, len(shapes))
	for k := range shapes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+shapes[k])
	}
	return strings.Join(parts, " ")
}

// judgeEnvelopes turns one escape shape's observed envelopes into an outcome.
//
// The rule is narrow on purpose. `recall` is excluded, because a query that legitimately reaches an
// AUTHORIZED record is supposed to return hits and excluding it is what keeps this from failing on
// something it is not measuring. What is judged is whether the two ADDRESSED tools — expand by id
// and re_read by path — materialized content for an address that escapes the root.
func judgeEnvelopes(shape, diagnosis string, observed map[string]string) (Outcome, string) {
	var served []string
	for tool, env := range observed {
		if tool == mcp.ToolRecall {
			continue
		}
		if strings.HasPrefix(env, "content") {
			served = append(served, tool)
		}
	}
	sort.Strings(served)

	if len(served) == 0 {
		return OutcomeVerified, fmt.Sprintf(
			"for %s, neither expand-by-id nor re_read-by-path materialized content for the escaping "+
				"address; no byte of the file outside the project root appeared in any response, no "+
				"refusal echoed the offending path, and the stored objects were untouched.", shape)
	}
	return OutcomeFailed, fmt.Sprintf(
		"for %s, %s answered with archived content instead of the explicit denial "+
			"internal/mcp/authorize.go describes. The bytes served were the ones captured from INSIDE "+
			"the project — the assertions above prove no byte of the outside file was returned — so "+
			"this is a policy divergence, not a data leak. %s",
		shape, strings.Join(served, " and "), diagnosis)
}

// normDiagnosis is the mechanism behind an unrefused link escape, stated once.
//
// It is finding S-1, and since Task 6 it is a REGRESSION diagnosis rather than a live one: it is
// rendered only on the `failed` branch, which now means the fix has come undone.
//
// The wording is the reviewer's correction and it matters: paths.Norm does NOT "leave a symlink
// unresolved". It calls EvalSymlinks and then DISCARDS a resolution that lands outside the root,
// keeping the unresolved spelling — which is a deliberate anti-smuggling rule pinned by
// internal/paths/norm_test.go, and it is still what Norm does. What changed is that authorization
// no longer rests on Norm alone: paths.ResolvesInside walks the path component by component
// through os.Readlink — junction-aware, which filepath.EvalSymlinks is not on Windows — and both
// retrieval call sites refuse a path that lands outside the root.
const normDiagnosis = "paths.Norm does not adopt an outside-landing resolution: it calls " +
	"EvalSymlinks and discards the result when it lands outside the root, keeping the unresolved " +
	"spelling, so the escaping path normalizes cleanly and Norm alone has nothing to refuse. Since " +
	"the S-1 fix, internal/mcp's authorizePath asks paths.ResolvesInside as well, and " +
	"internal/mcp/handlers_span.go's re_read gate — the second call site — asks it too. Content " +
	"served here means one of those two checks is gone. Owner: internal/paths + internal/mcp."

// lexicalDiagnosis is what a lexical `../` escape would mean if it were ever served. It is a
// different mechanism from normDiagnosis — Norm rejects `../` outright — so it gets its own
// sentence rather than borrowing one that would be false.
const lexicalDiagnosis = "paths.Norm rejects a lexically escaping path outright, so a response " +
	"carrying content for one would mean the authorization gate was not consulted at all. " +
	"Owner: internal/mcp."

// ---------------------------------------------------------------------------
// The store, read directly
// ---------------------------------------------------------------------------

// openStoreAt opens a real store over root. The caller is responsible for the single-writer
// discipline: a daemon must be stopped before this is used to write.
func openStoreAt(t *testing.T, root string) store.Store {
	t.Helper()
	s, err := store.Open(root, config.Defaults(), store.Deps{})
	if err != nil {
		t.Fatalf("security: store.Open(%s): %v", root, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// objectFingerprints hashes every file under objects/, so a case can prove a refused retrieval
// changed nothing.
func objectFingerprints(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	base := paths.Long(paths.Of(root).Objects)
	_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return nil //nolint:nilerr // a vanished entry is an observation, not a failure
		}
		b, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil
		}
		rel, relErr := filepath.Rel(base, p)
		if relErr != nil {
			return nil
		}
		sum := sha256.Sum256(b)
		out[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	return out
}

// requireObjectsUnchanged asserts every object present before is still present with the same bytes.
//
// It is a containment check rather than an equality one, deliberately: a daemon coming up under the
// retrieval may legitimately publish something new, and failing on that would be failing on the
// wrong thing. What may never happen is an existing object changing or disappearing, and that is
// exactly what this catches.
func requireObjectsUnchanged(t *testing.T, before, after map[string]string) {
	t.Helper()
	for rel, sum := range before {
		got, ok := after[rel]
		if !ok {
			t.Errorf("security: the stored object %s disappeared under a refused retrieval", rel)
			continue
		}
		if got != sum {
			t.Errorf("security: the stored object %s changed under a refused retrieval", rel)
		}
	}
}

// quarantineHolds reports whether .qompack/tmp/quarantine/ holds a file named for h.
func quarantineHolds(t *testing.T, root string, h core.Hash) bool {
	t.Helper()
	hx := strings.TrimPrefix(h.String(), "sha256:")
	for _, name := range relativeFiles(t, filepath.Join(paths.Of(root).Tmp, "quarantine")) {
		if strings.Contains(name, hx) {
			return true
		}
	}
	return false
}

// relativeFiles lists every regular file under dir, as slash-relative names. A missing directory is
// an empty list, not an error: several callers ask about a directory the product may never have
// created.
func relativeFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	base := paths.Long(dir)
	_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return nil //nolint:nilerr // a vanished entry is an observation, not a failure
		}
		if rel, relErr := filepath.Rel(base, p); relErr == nil {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// contains reports whether ss holds s.
func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// The durable-surface walker
// ---------------------------------------------------------------------------

// surfaceRoot is one tree to sweep.
type surfaceRoot struct {
	// Label names the surface when SplitBySegment is false.
	Label string
	Dir   string
	// SplitBySegment names each file's surface after its FIRST path segment under Dir, which is how
	// one walk of .qompack/ becomes eighteen separately-judged surfaces.
	SplitBySegment bool
}

// surfaceFile is one swept file: what it is, where it is, and the bytes that were actually examined.
type surfaceFile struct {
	Surface string
	Rel     string
	Content []byte
	// Unreadable is non-empty when the file was found but could not be opened, and Content is then
	// empty. It exists so that "nothing was found here" and "we could not look here" stay different
	// facts: a surface holding an unreadable file may not be reported clean without saying so.
	Unreadable string
	// Decoded records that Content is the DECOMPRESSED form of a .zst object. Without it a sweep of
	// objects/ would scan zstd frames, find nothing, and report a clean bill of health it never
	// earned — which is the single most important thing this walker gets right.
	Decoded bool
}

// walkSurfaces reads every regular file under each root, decompressing stored objects so their
// plaintext is what gets scanned.
func walkSurfaces(t *testing.T, roots []surfaceRoot) []surfaceFile {
	t.Helper()
	var out []surfaceFile
	for _, root := range roots {
		base := paths.Long(root.Dir)
		_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !d.Type().IsRegular() {
				return nil //nolint:nilerr // a vanished entry is an observation, not a failure
			}
			rel, relErr := filepath.Rel(base, p)
			if relErr != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)

			surface := root.Label
			if root.SplitBySegment {
				surface = strings.SplitN(rel, "/", 2)[0]
				if !strings.Contains(rel, "/") {
					surface = "dot-root"
				}
			}

			// A file that cannot be read is RECORDED, never dropped. Dropping it silently would let
			// a surface report itself clean on the strength of the files that happened to open,
			// which is the one way a sweep can lie without anybody writing a false assertion.
			raw, readErr := os.ReadFile(p)
			if readErr != nil {
				out = append(out, surfaceFile{
					Surface: surface, Rel: rel,
					Unreadable: readErr.Error(),
				})
				return nil
			}

			f := surfaceFile{Surface: surface, Rel: rel, Content: raw}
			if strings.HasSuffix(rel, ".zst") {
				if plain, decErr := store.Decode(raw); decErr == nil {
					f.Content, f.Decoded = plain, true
				}
			}
			out = append(out, f)
			return nil
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Surface != out[j].Surface {
			return out[i].Surface < out[j].Surface
		}
		return out[i].Rel < out[j].Rel
	})
	return out
}

// surfaceStat is one surface's size, for the evidence table.
type surfaceStat struct {
	Files int `json:"files"`
	Bytes int `json:"bytes"`
	// Skipped is how many files on this surface were found but could not be read.
	Skipped int `json:"skipped,omitempty"`
	// Names is every file swept, by path relative to its surface root. It is recorded because a
	// count alone cannot support a claim about a PARTICULAR file: "logs/ was swept and is clean"
	// and "logs/LOUD.log was swept and is clean" are different statements, and only the second is
	// worth making. A reader can check which one the evidence actually backs.
	Names []string `json:"names"`
}

// surfaceInventory counts what was swept, per surface, and names it.
func surfaceInventory(files []surfaceFile) map[string]surfaceStat {
	out := map[string]surfaceStat{}
	for _, f := range files {
		s := out[f.Surface]
		s.Files++
		s.Bytes += len(f.Content)
		if f.Unreadable != "" {
			s.Skipped++
		}
		s.Names = append(s.Names, f.Rel)
		out[f.Surface] = s
	}
	for k, s := range out {
		sort.Strings(s.Names)
		out[k] = s
	}
	return out
}

// sortedSurfaceNames is the inventory's keys, sorted.
func sortedSurfaceNames(inv map[string]surfaceStat) []string {
	out := make([]string, 0, len(inv))
	for k := range inv {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// requireSurfacePresent fails unless the named surface was actually swept. It is the non-vacuity
// guard: "no secret under objects/" means nothing if objects/ was empty.
func requireSurfacePresent(t *testing.T, files []surfaceFile, name string) {
	t.Helper()
	for _, f := range files {
		if f.Surface == name {
			return
		}
	}
	t.Fatalf("security: the sweep found no files under %s, so any clean result for it is vacuous", name)
}

// requireSurfaceFile fails unless one NAMED file was swept, and reports what was there instead.
//
// It exists because of an overclaim the independent review caught: the evidence said "logs/
// including LOUD.log swept and clean" while the only thing recorded was a file COUNT of one — and
// a day log alone accounts for one file. LOUD.log is written only when something Louds, so its
// presence has to be arranged and then checked, not assumed from a directory being non-empty.
// rel is the path AS THE SWEEP RECORDS IT — relative to the walked root, so a file on the `logs`
// surface of a `.qompack/` walk is "logs/LOUD.log" and not "LOUD.log".
func requireSurfaceFile(t *testing.T, files []surfaceFile, surface, rel string) {
	t.Helper()
	var present []string
	for _, f := range files {
		if f.Surface != surface {
			continue
		}
		if f.Rel == rel {
			return
		}
		present = append(present, f.Rel)
	}
	sort.Strings(present)
	t.Fatalf("security: %s was not swept under %s, so no claim may name it; the surface held %v",
		rel, surface, present)
}

// requireNothingSkipped fails when any swept file could not be read, because a surface holding one
// cannot be reported clean on the strength of the files that happened to open.
func requireNothingSkipped(t *testing.T, files []surfaceFile) {
	t.Helper()
	var skipped []string
	for _, f := range files {
		if f.Unreadable != "" {
			skipped = append(skipped, fmt.Sprintf("%s:%s (%s)", f.Surface, f.Rel, f.Unreadable))
		}
	}
	if len(skipped) > 0 {
		sort.Strings(skipped)
		t.Fatalf("security: %d swept file(s) could not be read, so no surface holding one may be "+
			"reported clean: %v", len(skipped), skipped)
	}
}

// surfacesContaining returns the surfaces whose swept bytes hold needle, as "surface:path" strings.
func surfacesContaining(files []surfaceFile, needle string) []string {
	var out []string
	for _, f := range files {
		for _, variant := range scanVariants(f.Content) {
			if strings.Contains(variant, needle) {
				out = append(out, f.Surface+":"+f.Rel)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// scanVariants is the forms of one file's bytes that are searched.
//
// The second variant is not decoration. Most of these surfaces are JSON or NDJSON, so a captured
// body reaches disk with its newlines and quotes escaped; a secret that spans a newline — a PEM
// block, most obviously — is present in the file and absent from a naive substring search. Undoing
// the three escapes that matter costs one pass and closes that hole.
func scanVariants(b []byte) []string {
	raw := string(b)
	if !strings.Contains(raw, `\n`) && !strings.Contains(raw, `\"`) {
		return []string{raw}
	}
	unescaped := strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\"`, `"`, `\\`, `\`).Replace(raw)
	return []string{raw, unescaped}
}

// placeholderRe matches an emitted redaction placeholder, in internal/redact's own spelling.
var placeholderRe = regexp.MustCompile(`«redacted:[a-z0-9_:]+»`)

// countPlaceholders counts redaction placeholders per surface. A surface with a positive count is
// one where redaction demonstrably ran; a surface with zero is one where either nothing sensitive
// ever reached it or nothing ever looked — the reason the evidence reports the number rather than a
// verdict.
func countPlaceholders(files []surfaceFile) map[string]int {
	out := map[string]int{}
	for _, f := range files {
		out[f.Surface] += len(placeholderRe.FindAll(f.Content, -1))
	}
	return out
}

// ---------------------------------------------------------------------------
// Secret fixtures and the scan
// ---------------------------------------------------------------------------

// secretRunLimit is the longest run of secret bytes §4.1 tolerates inside any durable artifact. The
// fragment window below is secretRunLimit+1 bytes wide, so a surface holding a longer contiguous run
// of a planted credential fails even when the whole literal is absent — which is what proves nothing
// reassembled secret material across a placeholder or a canonicalization boundary.
//
// It is this package's own copy of test/integration/storepipeline_test.go's constant, because that
// one is unexported in a _test.go file in another composition root.
const secretRunLimit = 8

// builtinSecretFamilies is the number of rules internal/redact ships (00-ARCHITECTURE.md §5.22a's
// nine families, with sk-ant- split out ahead of the generic sk- rule).
const builtinSecretFamilies = 10

// redactMarker is the ordinary prose the privacy sweep's recall searches for. It is deliberately
// NOT a secret: a query that had to name a credential to find its record would itself be the leak.
const redactMarker = "installing project dependencies"

// The two bounds an oversize hook delivery is measured against.
const (
	// hookOversizeBytes is the tool_response size the hard capture cap (4 MiB, internal/cli's
	// hookCaptureMaxBytes) must refuse.
	hookOversizeBytes = 6 << 20
	// hookPrefixProbeOffset is where the "must not survive" marker is planted inside an oversize
	// payload: far past the 4 KiB the refusal retains, so a prefix that grew a little would still
	// not reach it and the assertion stays about the order of magnitude the cap promises.
	hookPrefixProbeOffset = 64 << 10
)

// userPatternRegexp is the operator's own rule, declared in the project config so the capture path
// compiles it alongside the built-ins.
//
// It is deliberately NOT the secret itself: the config file is a durable surface this package
// sweeps, so a pattern that spelled the credential out would plant a permanent hit in the very tree
// being examined. Five characters of prefix is well under the fragment window.
const userPatternRegexp = `Zx7Qv[0-9][A-Za-z0-9]{12,}`

// secretSeed is one planted credential: the rule that should catch it, the line it is embedded in,
// and the credential-shaped run itself.
type secretSeed struct {
	Rule   string `json:"rule"`
	Line   string `json:"-"`
	Secret string `json:"-"`
}

// secretSeeds returns one planted instance of each built-in family, in internal/redact's own §5.22a
// rule order, plus the operator's pattern.
//
// Values come from internal/testutil's token table wherever it has one, so the credential-shaped
// runs in this tree stay in the one file that owns them. The four it has no token for are written
// split across a `+`: Go concatenates at compile time, so the runtime value is exact while no
// contiguous credential-shaped run appears in this source for a scanner to match. Every value is
// synthetic.
func secretSeeds(t *testing.T) []secretSeed {
	t.Helper()

	tok := func(name string) string {
		v, ok := testutil.SecretTokenValue(name)
		if !ok {
			t.Fatalf("security: internal/testutil has no secret token %s", name)
		}
		return v
	}

	// The base64 body is split mid-line for the same reason every credential literal in this tree
	// is: Go concatenates at compile time, so the runtime value is byte-exact while no contiguous
	// key-shaped run appears in this source for a scanner to match. The headers come from the token
	// table, which is where the tree keeps its credential markers.
	pem := tok("@@SEC_PEM_RSA_BEGIN@@") + "\n" +
		"MIIBOgIBAAJBAKj34Gkx" + "FhD90vcNLYLInFEX6Ppy1tPf9Cnzj4p4WGeKLs1Pt8Qu\n" +
		"KUpRKfFLfRYC9AIKjbJT" + "Wit+CqvjWYzvQwECAwEAAQJAIJLixBy2qpFoS4DSmoEm\n" +
		"o3qGy0t6z09AIJtH+5Oe" + "RV1be+N4cDYJKffGzDa88vQENZiRm0GRq6a+HPGQMd2k\n" +
		"TQ==\n" +
		tok("@@SEC_PEM_RSA_END@@")

	// No two planted secrets may share a run longer than secretRunLimit, or one survivor would be
	// reported as several. The three `sk-`/`ghp_` values are drawn from token-table entries chosen
	// for that: the classic PAT keeps the digits-then-alphabet body (its 38 characters are
	// load-bearing — see the eval-export finding), so the Anthropic and generic keys use the A/B
	// and the reversed-alphabet bodies instead of the same run.
	genericSK := "sk-" + "Zp4Mq7Xb2Nv9Kd6Gt3Rw8Ls"
	bearer := "mF9dXhKtbQvN" + "wPzYcRjLuGeSaBoTk"
	uriPassword := "tr0ub4dor8" + "gorgonzola"
	assignment := "9uPh0ldZ4qW7" + "eXcV1bNm6tYr"
	dotenv := "correcthorse" + "batterystaple91"
	userSecret := "Zx7Qv2Lm9" + "Rt4Wb6Yn1Ke8Pd"

	seeds := []secretSeed{
		{Rule: "pem_private_key", Line: pem, Secret: pem},
		{Rule: "aws_access_key_id", Line: "found credential " + tok("@@SEC_AWS_AKID@@") + " in the build environment", Secret: tok("@@SEC_AWS_AKID@@")},
		{Rule: "github_token", Line: "remote rejected: " + tok("@@SEC_GH_PAT@@"), Secret: tok("@@SEC_GH_PAT@@")},
		{Rule: "anthropic_key", Line: "export for the agent: " + tok("@@SEC_ANTHROPIC_AB@@"), Secret: tok("@@SEC_ANTHROPIC_AB@@")},
		{Rule: "generic_sk_key", Line: "vendor key " + genericSK, Secret: genericSK},
		{Rule: "jwt", Line: "session grant " + tok("@@SEC_JWT@@"), Secret: tok("@@SEC_JWT@@")},
		{Rule: "bearer_token", Line: "Authorization: Bearer " + bearer, Secret: bearer},
		{Rule: "credentialed_uri", Line: "postgres://deploy:" + uriPassword + "@db.internal:5432/prod", Secret: uriPassword},
		{Rule: "assignment_secret", Line: "client_secret=" + assignment, Secret: assignment},
		{Rule: "dotenv_value", Line: "DATABASE_PASSWORD=" + dotenv, Secret: dotenv},
		{Rule: "custom:0", Line: "deployment marker " + userSecret, Secret: userSecret},
	}
	return seeds
}

// secretTranscript embeds the planted lines in ANSI-coloured npm output with timestamps, the way a
// real `npm install` leak looks. The colouring wraps whole lines and never intersects a secret,
// because redaction runs on raw bytes BEFORE the ANSI canonicalizer strips the colour codes — an
// escape inside a token would defeat the rule, which is a different test's problem. The filler avoids
// every rule's mandatory prefilter literal so exactly the planted instances match.
func secretTranscript(seeds []secretSeed) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\x1b[2m2026-01-01T00:00:00.000Z\x1b[22m npm info %s\n", redactMarker)
	for i, seed := range seeds {
		fmt.Fprintf(&b, "\x1b[2m2026-01-01T00:00:%02d.%03dZ\x1b[22m npm warn %s\n",
			(i+1)%60, (i*137)%1000, seed.Rule)
		b.WriteString(seed.Line)
		b.WriteString("\n")
	}
	for i := 0; i < 128; i++ {
		fmt.Fprintf(&b, "added %d package%s in %dms\n", i, map[bool]string{true: "", false: "s"}[i == 1], 40+i)
	}
	return b.String()
}

// secretHit is one surviving credential run.
type secretHit struct {
	Surface string `json:"surface"`
	Rel     string `json:"path"`
	Rule    string `json:"rule"`
	// Kind is "literal" for the whole credential and "fragment" for a >secretRunLimit run of it.
	Kind   string `json:"kind"`
	Window string `json:"window"`
}

// scanForSecrets is the sweep itself: every planted credential against every swept byte, as a
// literal and as every window one byte longer than the tolerated run.
func scanForSecrets(files []surfaceFile, seeds []secretSeed) []secretHit {
	var hits []secretHit
	for _, f := range files {
		variants := scanVariants(f.Content)
		for _, seed := range seeds {
			found := false
			for _, v := range variants {
				if strings.Contains(v, seed.Secret) {
					hits = append(hits, secretHit{
						Surface: f.Surface, Rel: f.Rel, Rule: seed.Rule,
						Kind: "literal", Window: truncateWindow(seed.Secret),
					})
					found = true
					break
				}
			}
			if found {
				continue
			}
			for i := 0; i+secretRunLimit+1 <= len(seed.Secret); i++ {
				window := seed.Secret[i : i+secretRunLimit+1]
				for _, v := range variants {
					if strings.Contains(v, window) {
						hits = append(hits, secretHit{
							Surface: f.Surface, Rel: f.Rel, Rule: seed.Rule,
							Kind: "fragment", Window: truncateWindow(window),
						})
						found = true
						break
					}
				}
				if found {
					break
				}
			}
		}
	}
	return hits
}

// truncateWindow bounds what a record quotes back. A hit is evidence that a credential survived, and
// the evidence file is itself committed: reproducing the whole credential in it would be the same
// mistake one directory over.
// The bound is secretRunLimit itself, not a round number: §4.1 tolerates a run of that length
// inside a durable artifact, so quoting exactly that many bytes keeps the evidence file inside the
// same rule the evidence is checking, and a future sweep pointed at it would find nothing.
func truncateWindow(s string) string {
	if len(s) <= secretRunLimit {
		return s
	}
	return s[:secretRunLimit] + "…"
}

// hitsOn filters hits to one surface.
func hitsOn(hits []secretHit, surface string) []secretHit {
	var out []secretHit
	for _, h := range hits {
		if h.Surface == surface {
			out = append(out, h)
		}
	}
	return out
}

// renderHits is a bounded, deterministic rendering of a surface's hits for a record's reason line.
func renderHits(hits []secretHit) string {
	const shown = 6
	parts := make([]string, 0, shown)
	for i, h := range hits {
		if i == shown {
			parts = append(parts, fmt.Sprintf("… and %d more", len(hits)-shown))
			break
		}
		parts = append(parts, fmt.Sprintf("%s %s at %s", h.Rule, h.Kind, h.Rel))
	}
	return strings.Join(parts, "; ")
}

// surfaceOwner names the package that would own a fix for a hit on this surface. It is a table
// rather than a guess, because a returned finding without an owner is a complaint.
func surfaceOwner(surface string) string {
	switch surface {
	case "objects", "dag", "sketches", "grammar", "tmp", "migrate", "backup", "backup-tree":
		return "internal/store"
	case "index", "records", "checkpoints", "pins", "state":
		return "internal/store + internal/daemon"
	case "spool":
		return "internal/ipc + internal/cli"
	case "logs":
		return "internal/logging"
	case "metrics":
		return "internal/obs"
	case "eval", "eval-export":
		return "internal/eval"
	case "mcp-retrieval":
		return "internal/mcp"
	default:
		return "internal/cli"
	}
}

// sweepReport is the supporting artifact one sweep produces.
type sweepReport struct {
	Surfaces     map[string]surfaceStat `json:"surfaces"`
	Placeholders map[string]int         `json:"redaction_placeholders"`
	Hits         []secretHit            `json:"hits"`
	Seeds        []string               `json:"planted_rules"`
}

// writeSweepArtifact persists the sweep's own numbers beside the records, and returns its path.
func writeSweepArtifact(t *testing.T, rep sweepReport) string {
	t.Helper()
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		t.Fatalf("security: marshalling the sweep report: %v", err)
	}
	p := filepath.Join(artifactDir(t), "privacy-surface-sweep.json")
	if err := os.WriteFile(paths.Long(p), append(b, '\n'), 0o600); err != nil {
		t.Fatalf("security: writing %s: %v", p, err)
	}
	return filepath.Base(p)
}

// overlappingSeeds returns every pair of planted secrets that share a run longer than
// secretRunLimit, as "a/b" strings. A non-empty result means the fragment scan cannot attribute a
// hit to one family, so the evidence would name more survivors than there are.
func overlappingSeeds(seeds []secretSeed) []string {
	var out []string
	for i := range seeds {
		for j := i + 1; j < len(seeds); j++ {
			if sharesLongRun(seeds[i].Secret, seeds[j].Secret) {
				out = append(out, seeds[i].Rule+"/"+seeds[j].Rule)
			}
		}
	}
	sort.Strings(out)
	return out
}

// sharesLongRun reports whether a and b share any substring longer than secretRunLimit.
func sharesLongRun(a, b string) bool {
	for i := 0; i+secretRunLimit+1 <= len(a); i++ {
		if strings.Contains(b, a[i:i+secretRunLimit+1]) {
			return true
		}
	}
	return false
}
