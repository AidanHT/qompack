// Package platform is SP-17's deployment-environment matrix: the tests that decide, per operating
// system and per shape of path, whether the thing this repository actually ships can be installed
// and driven where a user would install it.
//
// # What makes this package different from test/e2e and test/canary
//
// Every other suite in this tree drives a binary produced by `go build ./cmd/qompack` into a
// temporary directory. That binary is not what a user runs. A user runs `bin/qompack[.exe]` from
// inside an assembled plugin bundle, reached through `${CLAUDE_PLUGIN_ROOT}` as the host expands
// it, with the manifest's own exec-form entries exactly as the host spawns them. So this
// package assembles the host target's bundle ONCE per test binary, through the very task Task 1
// committed (`go run ./tools/devtool bundle --target <os>/<arch> --out <dir>`), and every case
// below drives the binary out of that bundle. A `go build` here would test a different artifact
// and say nothing about the one that ships (packaging/README.md §5).
//
// # Outcomes, and why a skip is not a pass
//
// Each case writes one evidence record naming the target, the bundle it drove and what happened.
// The outcome vocabulary is deliberately three-valued: `verified` (the case ran and the product
// did what it promised), `failed` (the case ran and it did not — a returned finding, never fixed
// here), and `skipped` (the case did not run, and the reason says what was missing). A row that
// says nothing is a row nobody may cite. This is test/canary's rule (SP-19 M0-G2) applied to the
// platform axis instead of the capability axis.
//
// A `failed` record does NOT necessarily fail its Go test. Several cases exist precisely to
// MEASURE a known-unverified contract — the pre-C1.11 shell-form hook string under each Windows
// shell is the clearest (shell_test.go) — and the brief for this commit is explicit that
// such a defect is recorded with its owning package, not weakened into a passing assertion and not
// fixed from here. What each test does assert is the invariant that must hold regardless: hooks
// exit 0, stdout parses, and nothing is written outside the product write set.
//
// # Ownership
//
// Role B owns test/platform. Defects it finds are returned: manifest and hooks.json changes belong
// to internal/pluginmanifest (SP-17 Task 6), CLI changes to Task 5. Nothing in this package edits
// either.
//
// platform is a composition root (00-ARCHITECTURE.md §3.2): it imports the tree and nothing may
// import it. It has to be one for the same reason test/e2e and test/canary are — it spans daemon,
// ipc, paths, hookio, core and testutil directly and cli through the binary it spawns, and no
// internal package's allow-set permits that.
package platform

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
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

// ---------------------------------------------------------------------------
// Evidence records
// ---------------------------------------------------------------------------

// Outcome is what one platform case established. Three values, not a boolean: "we could not run
// this here" and "this ran and did the wrong thing" are different facts about a platform, and
// collapsing them is how an unrun case becomes evidence of support.
type Outcome string

// The three outcomes.
const (
	// OutcomeVerified: the case ran on this host and the product honoured its contract.
	OutcomeVerified Outcome = "verified"
	// OutcomeFailed: the case ran and it did not. Reason and Detail name the owner of the fix.
	OutcomeFailed Outcome = "failed"
	// OutcomeSkipped: the case did not run here. Reason says what was missing.
	OutcomeSkipped Outcome = "skipped"
)

// TargetInfo is the platform a record is attributed to. host_cli_version is the installed Claude
// CLI's version when one is on PATH and "unknown" when none is, rather than an empty string that
// would read as "we did not look".
type TargetInfo struct {
	OS             string `json:"os"`
	Arch           string `json:"arch"`
	Go             string `json:"go"`
	HostCLIVersion string `json:"host_cli_version"`
}

// BundleInfo identifies the artifact the case drove. The binary's digest is what makes a record
// re-checkable: two records naming the same version but different digests were produced by two
// different builds, and nothing else in the record would say so.
type BundleInfo struct {
	Version      string `json:"version"`
	BinarySHA256 string `json:"binary_sha256"`
}

// Record is one platform case's retained artifact.
type Record struct {
	Name   string     `json:"name"`
	Target TargetInfo `json:"target"`
	Bundle BundleInfo `json:"bundle"`
	// Outcome is required; writeRecord refuses a record that does not carry one.
	Outcome Outcome `json:"outcome"`
	// Reason is prose and it is the point: a skipped or failed case that does not say why is
	// indistinguishable from one nobody ran.
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

// artifactsEnvKey names the directory records are collected into. Unset (the ordinary `go test`
// case) means the test's own temp directory, so a plain run leaves nothing behind.
const artifactsEnvKey = "QOMPACK_PLATFORM_ARTIFACTS"

// artifactIndexName is the manifest of the records ONE run produced, written by TestMain.
//
// It exists because a record file is not self-dating: when $QOMPACK_PLATFORM_ARTIFACTS points at a
// committed evidence directory and a case fatals before writing, the previous run's file survives
// and presents a stale `verified` row that nothing distinguishes from a fresh one. Two mechanisms
// answer that together — the directory's records are pruned once at the start of a collecting run
// (collectDir), and this manifest states afterwards exactly which names that run produced.
const artifactIndexName = "INDEX.json"

var (
	// collectOnce guards the single prune-and-create of a collecting run's artifact directory.
	collectOnce sync.Once
	collectDir  string
	collectErr  error

	// tempArtifactMu guards tempArtifactDirs, which caches one temp directory per test so a test
	// writing three records does not scatter them across `…/001`, `…/002`, `…/003`.
	tempArtifactMu   sync.Mutex
	tempArtifactDirs = map[string]string{}

	// writtenMu guards writtenRecords, the names this run wrote, in write order.
	writtenMu      sync.Mutex
	writtenRecords []string
)

// artifactDir returns where records are written: $QOMPACK_PLATFORM_ARTIFACTS when it is set (so a
// collecting run can be committed), else one temp directory per test (so an ordinary `go test`
// leaves nothing behind).
func artifactDir(t *testing.T) string {
	t.Helper()
	if os.Getenv(artifactsEnvKey) != "" {
		collectOnce.Do(prepareCollectDir)
		if collectErr != nil {
			t.Fatalf("platform: %v", collectErr)
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
// The prune is deliberately unconditional over `*.json` in that directory: the alternative — pruning
// only the names this run is about to write — cannot remove a record whose CASE was deleted or
// renamed, which is exactly the stale row that reads as current. The directory is this package's
// alone (the caller points it at `commit2-platform-windows-amd64/`), and the manifest written at
// the end says what survived on purpose.
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
// absence in a document that was written afterwards rather than as nothing at all.
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
	_ = os.WriteFile(paths.Long(filepath.Join(collectDir, artifactIndexName)), append(b, '\n'), 0o600)
}

// newRecord returns a record pre-filled with this host's target and the assembled bundle's
// identity, so no case has to remember to attribute its own evidence.
func newRecord(t *testing.T, name string) Record {
	t.Helper()
	return Record{Name: name, Target: hostTarget(), Bundle: hostBundleInfo(t)}
}

// writeRecord persists rec as JSON under artifactDir and returns the path. The name is used as the
// file name, so it must be a single path segment: a record whose name carried a separator would
// silently write outside the artifact directory.
func writeRecord(t *testing.T, rec Record) string {
	t.Helper()

	if rec.Outcome == "" {
		t.Fatalf("platform %s: a record must carry an outcome", rec.Name)
	}
	if rec.Name == "" || rec.Name != filepath.Base(rec.Name) || strings.ContainsAny(rec.Name, `/\`) {
		t.Fatalf("platform: record name %q must be a single path segment", rec.Name)
	}
	if rec.Name+".json" == artifactIndexName {
		t.Fatalf("platform: %q collides with this run's own manifest", rec.Name)
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatalf("platform: marshalling the %s record: %v", rec.Name, err)
	}
	p := filepath.Join(artifactDir(t), rec.Name+".json")
	if err := os.WriteFile(paths.Long(p), append(b, '\n'), 0o600); err != nil {
		t.Fatalf("platform: writing %s: %v", p, err)
	}
	writtenMu.Lock()
	writtenRecords = append(writtenRecords, rec.Name)
	writtenMu.Unlock()
	t.Logf("platform %s: %s — %s\n  detail:   %s\n  artifact: %s",
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
// matrix in commit2-evidence.md is assembled from the records rather than from the log.
func skipRecorded(t *testing.T, rec Record, reason string) {
	t.Helper()
	rec.Outcome = OutcomeSkipped
	rec.Reason = reason
	writeRecord(t, rec)
	t.Skip(platformSkipPrefix + rec.Name + ": " + reason)
}

// ---------------------------------------------------------------------------
// Host identity
// ---------------------------------------------------------------------------

// claudeProbeBound bounds the one read-only `claude --version` this package runs.
const claudeProbeBound = 60 * time.Second

var (
	hostOnce    sync.Once
	hostCLIVers string
)

// claudeVersion returns the installed Claude CLI's version, or "unknown" when the CLI is not on
// PATH. `claude --version` is read-only: it starts no session, reads no project and changes no
// configuration, which is the whole of what this package is permitted to ask a live host.
func claudeVersion() string {
	hostOnce.Do(func() {
		hostCLIVers = "unknown"
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
		// The CLI answers "2.1.263 (Claude Code)"; the leading token is the version and the rest
		// is a product name the record already implies.
		raw := strings.TrimSpace(string(out))
		hostCLIVers = raw
		if fields := strings.Fields(raw); len(fields) > 0 {
			hostCLIVers = fields[0]
		}
	})
	return hostCLIVers
}

// hostTarget is the platform every record this run writes is attributed to.
func hostTarget() TargetInfo {
	return TargetInfo{
		OS:             runtime.GOOS,
		Arch:           runtime.GOARCH,
		Go:             runtime.Version(),
		HostCLIVersion: claudeVersion(),
	}
}

// ---------------------------------------------------------------------------
// The assembled bundle under test
// ---------------------------------------------------------------------------

// bundleAssembleBound is how long `devtool bundle` may take. It cross-compiles cmd/qompack and
// hashes the assembled tree; on a cold build cache, on a machine shared with another suite's
// whole-tree run, that is minutes rather than seconds.
const bundleAssembleBound = 15 * time.Minute

// bundle is the assembled host-target bundle every case in this package drives.
type bundle struct {
	// Dir is the bundle root — the directory a host would treat as ${CLAUDE_PLUGIN_ROOT}.
	Dir string
	// Bin is Dir/bin/qompack[.exe]: the binary the manifest names.
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
// document carries is ignored here and asserted by tools/devtool's own tests.
type bundleIdentity struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Target  struct {
		OS   string `json:"os"`
		Arch string `json:"arch"`
	} `json:"target"`
}

// assembledBundle assembles the host target's bundle once per test binary and returns it.
//
// Once, because assembling is a real cross-compile plus a hash of every file in the tree, and
// nothing any case does mutates the bundle — the one case that could (the plugin-root-with-spaces
// case) copies it first, and then asserts the ORIGINAL is byte-identical afterwards.
//
// The output directory outlives the test that triggered the assembly, so it is os.MkdirTemp rather
// than t.TempDir; removeBundle, called from TestMain, takes it away.
func assembledBundle(t *testing.T) bundle {
	t.Helper()
	bundleOnce.Do(doAssemble)
	if bundleErr != nil {
		t.Fatalf("platform: assembling the host bundle: %v", bundleErr)
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
// --version is passed: the version rule (packaging/README.md §3) is Task 1's, and pinning one here
// would make these records name a version no release ever carried.
func doAssemble() {
	repo, err := moduleRoot()
	if err != nil {
		bundleErr = err
		return
	}

	bundleBase, bundleErr = os.MkdirTemp("", "qompack-platform-bundle-")
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
	// HOME resolves a different module and build cache and can spend minutes re-downloading what
	// is already on disk (the reason test/canary's own doBuild gives).
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
// Insisting on exactly one is what keeps a stale directory from an earlier run — or a second
// target nobody asked for — from being picked up as the artifact under test.
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
// go.mod. `go run ./tools/devtool` is only meaningful from there, and a test's working directory is
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

// fileSHA256 returns the hex digest of a file's contents.
func fileSHA256(p string) (string, error) {
	b, err := os.ReadFile(paths.Long(p))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// copyTree copies src to dst recursively, preserving the executable bit. It is how a case puts the
// bundle somewhere awkward (a directory whose name has a space and non-ASCII characters) without
// disturbing the assembled original every other case drives.
func copyTree(src, dst string) error {
	return filepath.WalkDir(paths.Long(src), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(paths.Long(src), p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(paths.Long(target), 0o700)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(paths.Long(target), b, info.Mode().Perm())
	})
}

// ---------------------------------------------------------------------------
// Running the bundled binary
// ---------------------------------------------------------------------------

// runBound is how long one spawned qompack invocation may take. The longest manifest hook timeout
// is 20 s; anything past this is a hang, not a slow hook, and the generous margin is for a machine
// running another suite at the same time.
const runBound = 120 * time.Second

// clearedEnvPrefixes are the variable families a child of this package must not inherit from
// whoever launched `go test`.
//
// Both matter. Every `QOMPACK_` variable is a configuration or state input — an ambient
// `QOMPACK_RUNTIME__…`, `QOMPACK_PROJECT_ROOT`, `QOMPACK_IPC_ADDR` or the §12.3 fault-injection
// switch would
// silently change what this matrix measures, and `QOMPACK_PLATFORM_ARTIFACTS` is set for exactly
// the run that collects evidence. Every `CLAUDE_` variable is a host input, and `CLAUDE_PLUGIN_ROOT`
// in particular is the one thing the plugin-root cases set deliberately: inherited from an ambient
// shell it would point at the developer's real installed plugin, so the path and restriction cases
// would be observing a bundle that is not the one under test.
//
// Anything a case actually needs it passes in `env`, which is applied after the strip.
var clearedEnvPrefixes = []string{"QOMPACK_", "CLAUDE_"}

// run executes bin with args, feeding it stdin, and returns stdout, stderr and the exit code.
//
// The child's environment is the process environment MINUS clearedEnvPrefixes, plus whatever the
// caller names in env. dir is the child's working directory: it is a parameter rather than a
// constant because the project-root resolution order (internal/paths/resolve.go) consults the
// process's own cwd when no payload cwd decides, and several cases below exist to exercise that.
//
// A failure to START the process fails the test. A non-zero exit is RETURNED, never asserted here:
// the exit code is one of the things this matrix exists to observe.
func run(t *testing.T, bin, dir string, args []string, stdin []byte, env map[string]string) (stdout, stderr []byte, code int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), runBound)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // G204: a binary this package assembled, with arguments it chose
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = childEnv(env)

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &outBuf, &errBuf

	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		code = 0
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		t.Fatalf("platform: could not run %s %v: %v\nstderr:\n%s", bin, args, err, errBuf.String())
	}
	return outBuf.Bytes(), errBuf.Bytes(), code
}

// childEnv is the process environment with clearedEnvPrefixes removed and env layered on top.
//
// The match on the KEY is case-insensitive because Windows environment variable names are: an
// ambient `claude_plugin_root` is the same variable to the child as `CLAUDE_PLUGIN_ROOT`, and a
// case-sensitive strip would leave it in place on the one platform where it can differ in spelling
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

// hookSubcommands is §7.3's six hook entry points paired with the host event each answers. The
// list is written out rather than derived so that "all six hooks" cannot quietly become five.
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

// platformSessionID is the session every case in this package presents to a hook.
const platformSessionID = core.SessionID("sess-platform-0001")

// hookPayload renders the representative payload for one hook the way a host writes it: the §5.3
// fields that hook populates, and a cwd that is a NATIVE absolute path.
//
// The native path matters and is the reason this helper exists rather than a map literal at each
// call site: a hook refuses to create a store when the resolved project root does not exist, so a
// POSIX-flavoured "/c/Users/..." cwd on Windows resolves to a directory that is not there and the
// hook answers correctly while observing nothing — a green test that asserted nothing
// (test/e2e/hooks_test.go's eventFor makes the same point).
func hookPayload(t *testing.T, event, cwd string) []byte {
	t.Helper()
	if !filepath.IsAbs(cwd) {
		t.Fatalf("platform: hook payload cwd %q must be a native absolute path", cwd)
	}
	e := hookio.Event{
		HookEventName:  event,
		SessionID:      platformSessionID,
		TranscriptPath: filepath.Join(cwd, "transcript.jsonl"),
		CWD:            cwd,
	}
	switch event {
	case "PostToolUse":
		e.ToolName = "Read"
		e.ToolUseID = core.ToolUseID("toolu_01A2B3C4D5E6F7G8H9J0K1L2")
		e.ToolInput = json.RawMessage(`{"file_path":"src/auth.ts"}`)
		e.ToolResponse = json.RawMessage(`{"content":"export const auth = 1;"}`)
	case "UserPromptSubmit":
		e.Prompt = "why does the auth middleware reject an expired token twice?"
	case "SessionStart":
		e.Source = "startup"
	case "PreCompact":
		e.Trigger = "auto"
	case "Stop", "SubagentStop":
		e.StopHookActive = true
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("platform: marshalling a %s payload: %v", event, err)
	}
	return b
}

// parseHookOutput asserts stdout is something a host could consume and returns it decoded.
//
// §2.3's always-exit-0 rule is only half the hook contract: a hook that exits 0 while writing
// garbage to stdout breaks the host just as thoroughly. An EMPTY stdout is accepted and decodes to
// the zero Output — several degraded paths legitimately answer with nothing at all.
func parseHookOutput(t *testing.T, what string, stdout []byte) hookio.Output {
	t.Helper()
	trimmed := bytes.TrimSpace(stdout)
	var out hookio.Output
	if len(trimmed) == 0 {
		return out
	}
	if err := json.Unmarshal(trimmed, &out); err != nil {
		t.Fatalf("platform: %s wrote stdout that is not a hookio.Output: %v\nstdout:\n%s", what, err, stdout)
	}
	return out
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

// jsonUnmarshalTrimmed decodes b into v after trimming surrounding whitespace, so a command that
// ends its document with a newline (every `--json` mode here does) still decodes cleanly.
func jsonUnmarshalTrimmed(b []byte, v any) error {
	return json.Unmarshal(bytes.TrimSpace(b), v)
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

// newProjectAt creates a project rooted at exactly the path the caller names, with a sibling HOME.
//
// Two decisions are load-bearing, and both are test/canary's, for its reasons.
//
// The enclosing directory is os.MkdirTemp rather than t.TempDir: t.TempDir fails the test if its
// cleanup cannot remove the tree, and on Windows a daemon that has not finished unwinding — or a
// deny-ACE this case itself placed — makes that a certainty rather than a risk. Removal here is
// best-effort, and it is preceded by a permission reset so a deny this case installed can never
// make the cleanup fail (test/e2e/faultinject_test.go's resetPermissionsForCleanup, cloned because
// test/e2e is a composition root and nothing may import one).
//
// The `.git` marker stops paths.Resolve's upward walk at this root rather than letting it escape
// into whatever encloses the OS temp directory, which is what makes a payload cwd — rather than
// the environment override — able to decide the root in the cases that need it to.
func newProjectAt(t *testing.T, base, rootName string) project {
	t.Helper()

	root := filepath.Join(base, rootName)
	home := filepath.Join(base, "home")
	for _, d := range []string{root, home} {
		if err := os.MkdirAll(paths.Long(d), 0o700); err != nil {
			t.Fatalf("platform: creating %s: %v", d, err)
		}
	}
	if err := os.MkdirAll(paths.Long(filepath.Join(root, ".git")), 0o700); err != nil {
		t.Fatalf("platform: creating the .git marker under %s: %v", root, err)
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

// tempBase returns a disposable enclosing directory with the permission-resetting cleanup
// registered BEFORE anything is created inside it, so the reset always runs first.
func tempBase(t *testing.T) string {
	t.Helper()
	base, err := os.MkdirTemp("", "qompack-platform-")
	if err != nil {
		t.Fatalf("platform: creating a temporary directory: %v", err)
	}
	t.Cleanup(func() {
		resetPermissionsForCleanup(base)
		_ = os.RemoveAll(paths.Long(base))
	})
	return base
}

// newProject is newProjectAt over a fresh temp base, for a case that does not care what the root
// is called.
func newProject(t *testing.T, rootName string) project {
	t.Helper()
	return newProjectAt(t, tempBase(t), rootName)
}

// ---------------------------------------------------------------------------
// Permissions
// ---------------------------------------------------------------------------

// windowsDenyMask is the deny-ACE this package applies: the specific write, delete and
// entry-creation rights, and NOTHING ELSE.
//
// It is deliberately not icacls' simple `W` right, which is what test/e2e's obsDenyWrites uses.
// `W` is FILE_GENERIC_WRITE, and that mask includes READ_CONTROL and SYNCHRONIZE — two rights a
// reader needs. Denying them has consequences a "read-only directory" is not supposed to have, and
// both of them were measured here before this constant existed:
//
//   - os.ReadDir on a directory beneath the deny fails, because Go opens a directory handle with
//     SYNCHRONIZE. The read-only-.qompack case below compares directory fingerprints before and
//     after the deny; with `W` every fingerprint flipped to "absent" and the case reported that it
//     had FOUND degradation evidence. It had found its own blindness.
//   - CreateProcess on a binary beneath the deny fails with "Access is denied", because the image
//     is opened with SYNCHRONIZE too. That made the read-only-bundle case unable to run the
//     product at all, which is not the restriction it means to impose: a managed install is
//     non-writable and still executable.
//
// The named rights below are icacls' own spellings: WD write-data/add-file, AD append-data/add-
// subdirectory, WEA write-extended-attributes, WA write-attributes, DE delete, DC delete-child.
const windowsDenyMask = ":(OI)(CI)(WD,AD,WEA,WA,DE,DC)"

// denyWrites makes dir non-writable for the current user while leaving it readable, listable and
// executable: a real deny-ACE via icacls on Windows (the FILE_ATTRIBUTE_READONLY bit is a
// near-no-op for directories there — the same mechanism internal/cli's spool-readonly fault site
// uses), plain permission bits elsewhere.
//
// On POSIX 0o500 has the property the Windows mask is built to match: the directory can be
// traversed and listed, its existing files keep their own modes (so a binary beneath it stays
// executable), and nothing new can be created in it.
func denyWrites(dir string) error {
	if runtime.GOOS == "windows" {
		u, err := user.Current()
		if err != nil {
			return err
		}
		// (OI)(CI) makes the ACE inheritable, and setting it propagates to the entries that
		// already exist beneath dir, so one call covers the whole tree.
		return exec.Command("icacls", dir, "/deny", u.Username+windowsDenyMask).Run() //nolint:gosec // G204: fixed subcommand over this package's own temp directory
	}
	// The POSIX side has to walk, because chmod is not inheritable. A single chmod on the top
	// directory would leave every subdirectory writable, and the read-only-bundle case — which
	// denies the install directory and then expects `bin/` to be non-writable too — would quietly
	// be testing nothing. Directories become r-x so they can still be traversed and listed; files
	// lose their write bits and keep their execute bit, which is what keeps the bundled binary
	// runnable under a deny exactly as the Windows mask does.
	return filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.Chmod(p, 0o500)
		}
		return os.Chmod(p, info.Mode().Perm()&^0o222)
	})
}

// denyProbeName is the throwaway file denyBites tries to create.
const denyProbeName = "qompack-deny-probe.tmp"

// denyBites checks, from this process, that a deny actually took effect — and that it took effect
// on writes ONLY.
//
// Both halves are necessary and both were learned here. A deny that silently did nothing would make
// every restriction case a second copy of the unrestricted run, passing for the wrong reason. And a
// deny that also blocked READING would make the degradation-evidence search report its own
// blindness as a finding, which is exactly what icacls' simple `W` right produced before
// windowsDenyMask replaced it.
//
// The two failures are answered differently, and deliberately. A deny that blocks listing is a
// defect in THIS package's mask, so it fails the test immediately. A deny that does not bite is a
// fact about the environment — a process running as root ignores POSIX mode bits, and a privileged
// Windows token can ignore a deny ACE — so it is reported to the caller, which records a skip. A
// hard failure there would turn "this host cannot express a read-only directory to us" into "the
// product is broken", which is the one thing the outcome vocabulary exists to prevent.
func denyBites(t *testing.T, dir string) bool {
	t.Helper()
	if _, err := os.ReadDir(paths.Long(dir)); err != nil {
		t.Fatalf("platform: the deny on %s also blocked listing it (%v); a read-only directory must "+
			"stay readable, or this case measures its own blindness rather than the product", dir, err)
	}
	probe := filepath.Join(dir, denyProbeName)
	if err := os.WriteFile(paths.Long(probe), []byte("probe"), 0o600); err == nil {
		_ = os.Remove(paths.Long(probe))
		return false
	}
	return true
}

// resetPermissionsForCleanup best-effort undoes anything a case did to dir's permissions, so the
// enclosing RemoveAll is never blocked by a deny this package installed itself. It is registered
// before the directory is populated, so it always runs first.
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

// caseInsensitiveFS reports whether the filesystem holding dir resolves two case spellings of one
// name to the same file. It is DETECTED rather than inferred from runtime.GOOS: macOS ships
// case-insensitive by default and case-sensitive on request, a Windows volume can be marked
// case-sensitive per directory, and a Linux checkout can live on a mounted case-insensitive
// filesystem. The mixed-case case below asserts whichever of the two the running filesystem
// actually exhibits, and records which it saw.
func caseInsensitiveFS(t *testing.T, dir string) bool {
	t.Helper()
	probe := filepath.Join(dir, "QompackCaseProbe")
	if err := os.WriteFile(paths.Long(probe), []byte("probe"), 0o600); err != nil {
		t.Fatalf("platform: writing the case-sensitivity probe in %s: %v", dir, err)
	}
	defer func() { _ = os.Remove(paths.Long(probe)) }()
	_, err := os.Stat(paths.Long(filepath.Join(dir, "qompackcaseprobe")))
	return err == nil
}

// ---------------------------------------------------------------------------
// Write-set confinement
// ---------------------------------------------------------------------------

// snapshotTree hashes every regular file under the given roots.
//
// Content hashing rather than mtime comparison is deliberate, for test/guards/writeset_test.go's
// reason: mtime resolution is coarse enough on some filesystems that a rewrite within the same
// tick would go unnoticed, which is exactly the case this check exists to catch.
//
// Keys are the file's path under the root AS THE CALLER SPELLED IT, not as the walk saw it. The
// walk has to start from paths.Long(root) — a root past MAX_PATH is unwalkable otherwise — and on
// Windows that prefixes every yielded path with `\\?\`, which would then match none of the
// permitted write-set prefixes and turn this guard into a list of false offenders. Rebuilding the
// key from the caller's spelling keeps the two comparable on every path length.
//
// Unlike the guard's version this one TOLERATES per-entry errors. The guard drives the hooks in
// process, so nothing else is writing while it walks; here a daemon may still be unwinding, and a
// file that vanishes between the directory read and the open is a normal observation rather than a
// failure. A path that cannot be read is simply absent from the snapshot, which can only make the
// comparison below report a difference — never hide one.
func snapshotTree(roots ...string) map[string]string {
	got := map[string]string{}
	for _, root := range roots {
		base := paths.Long(root)
		_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil //nolint:nilerr // a vanished or unreadable entry is an observation, not a failure
			}
			if d.IsDir() || !d.Type().IsRegular() {
				return nil
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
			got[filepath.Join(root, rel)] = hex.EncodeToString(sum[:])
			return nil
		})
	}
	return got
}

// underAny reports whether p sits under one of the given directory prefixes.
func underAny(p string, prefixes []string) bool {
	for _, pre := range prefixes {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	return false
}

// writeSetOffenders returns every path created, modified or deleted between before and after that
// does not sit under one of the permitted prefixes, sorted.
func writeSetOffenders(before, after map[string]string, permitted []string) []string {
	var out []string
	for p, sum := range after {
		if underAny(p, permitted) {
			continue
		}
		if old, ok := before[p]; !ok || old != sum {
			out = append(out, p)
		}
	}
	for p := range before {
		if underAny(p, permitted) {
			continue
		}
		if _, ok := after[p]; !ok {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// productWriteSet is the two directories §13 invariant 7 permits a hook to write under, for one
// project root and one home. Everything else under either tree is an offender.
func productWriteSet(root, home string) []string {
	return []string{
		paths.Of(root).Dot + string(os.PathSeparator),
		paths.Of(home).Dot + string(os.PathSeparator),
	}
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
	// far longer than daemon.SpawnPollBound on purpose: session-start's own EnsureRunning has
	// already waited that long before returning, so anything still outstanding here is a cold
	// start on a loaded machine, and a false negative would read as an unsupported platform.
	daemonUpBound = 60 * time.Second
	// daemonPollTick is the interval every poll in this file re-asks on. A ticker, not
	// time.Sleep, per §6.1's wall-clock-sleep ban (devtool lint's sleepcheck sub-check).
	daemonPollTick = 100 * time.Millisecond
	// daemonDownBound is how long shutdownIfReachable retries admin.shutdown before giving up and
	// saying so.
	daemonDownBound = daemon.StopCleanupBound + 15*time.Second
)

// waitDaemonUp polls root's resolved address until something answers, and reports whether it did.
func waitDaemonUp(t *testing.T, root string) (ipc.Addr, bool) {
	t.Helper()
	addr, err := ipc.Resolve(root)
	if err != nil {
		t.Fatalf("platform: ipc.Resolve(%s): %v", root, err)
	}
	if ipc.Probe(addr, probeTimeout) {
		return addr, true
	}
	ticker := time.NewTicker(daemonPollTick)
	defer ticker.Stop()
	deadline := time.NewTimer(daemonUpBound)
	defer deadline.Stop()
	for {
		select {
		case <-ticker.C:
			if ipc.Probe(addr, probeTimeout) {
				return addr, true
			}
		case <-deadline.C:
			return addr, false
		}
	}
}

// adminPing sends one admin.ping and reports whether the daemon answered OK.
//
// Client.Send never propagates a transport error — a failed round trip spools the request and
// returns silently (§2.4/§12.3) — so the ANSWER, not the error, is the signal: a response that is
// OK came from a live daemon, and one that is not came from the spool.
func adminPing(t *testing.T, root string, addr ipc.Addr) (ipc.Response, bool) {
	t.Helper()
	sp, _ := ipc.NewSpool(paths.Of(root).Spool)
	c := ipc.NewClientWithOptions(addr, sp, nil, nil, ipc.ClientOptions{
		ProjectRoot:     root,
		ConnectDeadline: roundTripDeadline,
		AckDeadline:     roundTripDeadline,
	})
	defer func() { _ = c.Close() }()

	resp, _ := c.Send(context.Background(), ipc.Request{
		Op: ipc.OpAdminPing, TS: core.NowMilli(core.SystemClock()), Reply: true,
	}, roundTripDeadline)
	return resp, resp.OK
}

// shutdownIfReachable dials root's resolved address and, if anything answers or a live process
// still holds the lock, sends admin.shutdown until the daemon goes away.
//
// This is test/e2e's e2eShutdownIfReachable reduced to what this package needs, cloned rather than
// imported because test/e2e is a composition root. The two properties worth keeping are the ones
// its own comments were written around: "gone" is the LOCK disappearing rather than the address
// going unreachable, because Stop closes the listener first and then goes on writing under
// .qompack/ for the rest of its unwind; and a daemon that is still COMING UP holds the lock and
// its day log while answering no dial at all, so liveness of the lock holder — not reachability —
// decides whether there is anything to wait for.
func shutdownIfReachable(t *testing.T, root string) {
	t.Helper()
	addr, err := ipc.Resolve(root)
	if err != nil {
		return
	}
	reachable := ipc.Probe(addr, probeTimeout)
	if !reachable {
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
			t.Logf("platform: a daemon (lock pid %d, shutdown pid %d) still held %s after %s of "+
				"retried admin.shutdown; this case's cleanup is about to remove a tree it may still be writing to",
				lockPID, shutdownPID, daemon.LockPath(root), daemonDownBound)
			return
		}
	}
}

// processSettled reports whether the pid that held the lock can no longer write inside the tree.
// Our own pid always can, and asking whether the asking process has exited is meaningless, so for
// that one case the lock's absence is the whole condition (test/e2e's e2eShutdownProcessSettled
// makes the same distinction for the same reason).
func processSettled(shutdownPID int) bool {
	if shutdownPID == 0 || shutdownPID == os.Getpid() {
		return true
	}
	return !testutil.ProcessAlive(shutdownPID)
}

// daemonHoldingLock reports the pid recorded in root's daemon.lock and whether a live process
// still holds it.
//
// The lock is read with paths.ReadFileShared, whose handle carries FILE_SHARE_DELETE, so polling
// it cannot make the daemon's own Release fail on Windows and thereby CAUSE the abandoned lock it
// is checking for. A lock file that exists but does not parse counts as held: paths.CreateNew
// creates the file and only then writes the body, so an empty daemon.lock is one a process
// finished creating microseconds ago — the most alive a daemon ever is.
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
// Path shapes
// ---------------------------------------------------------------------------

// The shapes of an awkward project root, named once so the matrix table in commit2-evidence.md and
// the tests cannot disagree about what was exercised.
const (
	// plainRootName is the control: an ordinary ASCII directory name.
	plainRootName = "plain-project"
	// spacedRootName is the `C:\Program Files\…` / `~/Library/Application Support/…` shape. The
	// odd spelling is deliberate and is the point: two spaces, so a launcher that quotes only the
	// first word is caught, and the fragments are not words a shell could coincidentally resolve.
	spacedRootName = "my proj ect" //nolint:misspell // "proj ect" is a directory name with a space in it, not the word "etc"
	// unicodeRootName covers a Latin-1 supplement character, CJK, and a combining-prone vowel.
	unicodeRootName = "prøjekt-日本-ünï"
	// mixedCaseRootName is created with this spelling and additionally invoked all-lowercase.
	mixedCaseRootName = "Proj-Case"
	// pluginRootName is where the bundle is copied for the plugin-root cases: a space AND
	// non-ASCII, because packaging/README.md §7 lists them as two separate open questions and a
	// single directory answers both in one run.
	pluginRootName = "plug in ünï"
)

// longRootSegments and longRootSegmentLen build a root whose total path exceeds Windows' MAX_PATH,
// in internal/testutil/winpath.go's longPathName shape (12 segments of 25 characters, which clears
// 260 with room for the temp-directory prefix and everything the product appends beneath it).
const (
	longRootSegments   = 12
	longRootSegmentLen = 25
	// maxPathThreshold is the Windows MAX_PATH limit the long-root case must exceed. It is only
	// ever compared against, never used to build a path.
	maxPathThreshold = 260
)

// longRootName returns the deep relative path the long-root case creates under its temp base.
func longRootName() string {
	seg := strings.Repeat("d", longRootSegmentLen)
	parts := make([]string, longRootSegments)
	for i := range parts {
		parts[i] = seg
	}
	return filepath.Join(parts...)
}
