// Package fault is SP-17's fault-and-recovery matrix: the tests that decide whether the thing this
// repository actually ships comes back from a forced failure at a publication boundary, from a
// lifecycle event that never arrived or arrived twice, from a child that died mid-request, and from
// a disk, a directory or a lock that would not cooperate.
//
// # What makes this package different from the suites beside it
//
// internal/daemon cuts its delivery path in process at every step. internal/checkpoint proves a
// failed publication retains the previous checkpoint. test/e2e kills a real daemon and replays. None
// of them drives the ARTIFACT a user installs, and none of them asks the question acceptance row
// SP17-M7-04 actually asks: after the cut and after the recovery, does anything under `.qompack/`
// still point at something that is not there? This package assembles the host target's plugin bundle
// once per test binary through Task 1's own `devtool bundle`, drives that bundle's `bin/qompack` and
// its real detached daemon for every scenario, and then walks the whole product write set looking
// for references that do not resolve.
//
// # Outcomes, and why a skip is not a pass
//
// Each case writes one evidence record naming the boundary it cut, how it seeded the state, what
// happened, and how many dangling references the audit found before and after. The vocabulary is
// four-valued:
//
//   - `recovered`: the cut healed. The audit names no dangling reference the cut introduced, and the
//     product went on recording.
//   - `explicit_incomplete`: the cut did NOT heal, and the product SAYS SO — a gap in
//     `qompack status --json`, a failing row in `self-test --json`, a LOUD.log line, or a
//     checkpoint DropEntry. §13 invariant 10: degradation is loud. This is a pass, and it is the
//     honest answer for a boundary whose state genuinely cannot be reconstructed.
//   - `failed`: the cut did not heal and nothing said so, or a reference dangles that nothing
//     reports. A measured divergence with an owner. Defects are RETURNED with their owning package
//     (store, checkpoint, daemon, cli, mcp) and never fixed from here.
//   - `skipped`: the case did not run here, and the reason says what was missing. A skip is never
//     verified and never recovered.
//
// A `failed` record does not automatically fail its Go test. Where the finding is that a REFERENCE
// DANGLES SILENTLY — the acceptance row's own words — the Go test fails too, because a pointer to
// nothing that nothing reports is the defect this package exists to find. Where the finding is that
// the product reported the gap through a channel this package did not expect, the record is the
// deliverable and the case goes on. Each site says which it is.
//
// # The daemon-side fault seam that does not exist
//
// The fault-injection switch internal/cli/fault.go reads is a HOOK-side seam, and this file never
// writes its name out: test/guards/faultenv_test.go confines that spelling to two non-test files and
// matches on substring (evidence_test.go says more about why that matters here).
// internal/daemon/spawn.go's buildSpawnEnv strips the variable
// case-insensitively from every daemon it spawns, and test/guards/faultenv_test.go's
// TestGuard_FaultEnvIsConfinedToTwoFiles forbids a second spelling anywhere else. Nothing here
// extends it. A daemon-side cut in this package is therefore one of exactly two things: a real kill
// of a real daemon this package started in one of its own `qompack-fault-` fixtures, or a mutation
// of the files the daemon had already written. That is a limitation of the coverage and it is
// recorded as one, not worked around.
//
// # Ownership
//
// Role D owns test/fault. `qompack fsck` does not exist yet (Task 5 builds it): the "no newly
// dangling pointer" check here is auditProject, a test helper, and the report says which of its
// checks fsck should absorb.
//
// fault is a composition root (00-ARCHITECTURE.md §3.2): it imports the tree and nothing may import
// it. It reaches store, checkpoint, daemon, ipc, paths, config, core and testutil directly and cli
// through the binary it spawns, and no internal package's allow-set permits that.
package fault

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
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
)

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
	// in exec form as `<bundle>/bin/qompack mcp`, with no shell in between.
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
		t.Fatalf("fault: assembling the host bundle: %v", bundleErr)
	}
	return hostBundle
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

	bundleBase, bundleErr = os.MkdirTemp("", "qompack-fault-bundle-")
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

// childWaitDelay bounds how long Wait may go on after a child has exited.
const childWaitDelay = 5 * time.Second

// slowChildNotice is the elapsed time past which one invocation is logged with its duration. It is
// diagnostics, never an assertion: this package makes no timing claim at all.
const slowChildNotice = 5 * time.Second

// clearedEnvPrefixes are the variable families a child of this package must not inherit from
// whoever launched the test binary. An ambient fault-injection switch would silently change what
// every row here measures, and this package's own artifacts-directory variable is set for exactly
// the run that collects evidence and must not reach a child at all; an inherited CLAUDE_PLUGIN_ROOT would point at the developer's
// real installed plugin rather than at the bundle under test. Anything a case actually needs it
// passes in env, which is applied after the strip.
var clearedEnvPrefixes = []string{"QOMPACK_", "CLAUDE_"}

// run executes bin with args, feeding it stdin, and returns stdout, stderr and the exit code.
//
// A failure to START the process fails the test. A non-zero exit is RETURNED, never asserted here:
// several cases exist to observe an exit code rather than to demand one.
//
// All three streams are real FILES rather than pipes, and that is load-bearing (Task 3 learned it
// the hard way). Handing exec.Cmd an io.Reader/io.Writer makes it create an OS pipe and a copying
// goroutine, and Wait does not return until that goroutine finishes — which means until the pipe's
// other end is closed by every process holding it. A hook spawns a DETACHED DAEMON; a daemon that
// inherits any of the hook's three standard handles keeps the pipe open after the hook has exited,
// and Wait then blocks for the daemon's whole lifetime. An *os.File is passed as a plain descriptor
// with no copier, so Wait returns when the PROCESS exits. WaitDelay is the second belt.
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
		// point. CommandContext kills the child when the context expires, so Run returns the dead
		// process's *exec.ExitError — never ctx.Err() — and an errors.As arm placed first would
		// swallow every timeout as an ordinary non-zero exit.
		t.Fatalf("fault: %s %v did not finish within %s (elapsed %s); stderr:\n%s",
			filepath.Base(bin), args, runBound, elapsed.Round(time.Millisecond), stderr)
	case errors.Is(err, exec.ErrWaitDelay):
		code = 0
		t.Logf("fault: %s %v exited 0 but its I/O was still open after %s (WaitDelay fired) — "+
			"a process it spawned inherited a standard handle; elapsed %s",
			filepath.Base(bin), args, childWaitDelay, elapsed.Round(time.Millisecond))
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		t.Fatalf("fault: could not run %s %v: %v\nstderr:\n%s", bin, args, err, stderr)
	}
	if elapsed > slowChildNotice {
		t.Logf("fault: %s %v took %s", filepath.Base(bin), args, elapsed.Round(time.Millisecond))
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
	dir, err := os.MkdirTemp("", "qompack-fault-stdio-")
	if err != nil {
		t.Fatalf("fault: creating a stdio directory: %v", err)
	}
	c := childStdio{dir: dir, outPath: filepath.Join(dir, "stdout"), errPath: filepath.Join(dir, "stderr")}

	inPath := filepath.Join(dir, "stdin")
	if err := os.WriteFile(inPath, stdin, 0o600); err != nil {
		t.Fatalf("fault: writing the child's stdin: %v", err)
	}
	if c.in, err = os.Open(inPath); err != nil {
		t.Fatalf("fault: opening the child's stdin: %v", err)
	}
	if c.out, err = os.Create(c.outPath); err != nil {
		t.Fatalf("fault: creating the child's stdout: %v", err)
	}
	if c.err, err = os.Create(c.errPath); err != nil {
		t.Fatalf("fault: creating the child's stderr: %v", err)
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

// childEnv is the process environment with clearedEnvPrefixes removed and env layered on top. The
// match on the KEY is case-insensitive because Windows environment variable names are
// (internal/daemon/spawn.go's buildSpawnEnv makes the same point about the fault switch).
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
//
// It t.Errorf's rather than t.Fatalf's. Every row here goes on to audit the store and record its
// evidence, and a hook that broke invariant 6 is a finding the record must still carry — a Fatal
// would take the row's own evidence down with it.
func runHook(t *testing.T, bin string, p project, argv []string, payload []byte) {
	t.Helper()
	runHookWithEnv(t, bin, p, argv, payload, nil)
}

// runHookWithEnv is runHook with extra environment layered over the project's own — the shape every
// fault-injected row needs, since the seam is read once per process from the environment.
func runHookWithEnv(t *testing.T, bin string, p project, argv []string, payload []byte, extra map[string]string) {
	t.Helper()
	stdout, stderr, code := run(t, bin, p.Root, argv, payload, p.EnvWith(extra))
	if code != 0 {
		t.Errorf("fault: %v must exit 0, got %d (§13 invariant 6)\nstdout:\n%s\nstderr:\n%s",
			argv, code, stdout, stderr)
	}
	if !emptyOrParseableJSON(stdout) {
		t.Errorf("fault: %v wrote stdout a host cannot parse:\n%s", argv, stdout)
	}
}

// runFlush drives the SessionEnd flush hook for sess and waits for the daemon to end the session.
//
// Since C1.15 the hook answers as soon as the flush is durable — Claude Code gives a plugin's
// SessionEnd hooks one shared 1.5 s budget and cancels a hook still running when it runs out — and the
// daemon ends the session on a goroutine of its own (internal/daemon/session_end.go). The hook's exit
// therefore no longer says the session's deliveries have all published or that SessionEnd has run,
// which is what every row that audits the store right after its flush relied on. The end writes the
// terminal-hook marker (contract.MarkerPath) right after SessionEnd, which it runs only once the
// session's earlier deliveries are on the committed frontier (settleSession); so a marker naming sess,
// and not the one that stood before the hook ran, is the daemon's own record of both.
//
// It reports rather than fatals, like waitIndexed: a row whose daemon never ends the session is a
// finding its record must still carry.
func runFlush(t *testing.T, b bundle, p project, sess core.SessionID) {
	t.Helper()
	if !flushAndAwaitEnd(t, b, p, sess) {
		t.Errorf("fault: the daemon did not end session %s within %s of its flush hook "+
			"(no terminal-hook marker naming it)", sess, indexBound)
	}
}

// flushAndAwaitEnd is runFlush's body without the verdict: it drives the flush hook and reports
// whether the daemon ended the session within indexBound. A caller whose cut may legitimately leave
// the product unable to take the flush at all (recoverSession) reads the answer as a measurement.
//
// The session has ended when the marker names it AND it has left the daemon's recovery record. The
// marker alone is not enough: endSession writes it before its final drain, which publishes what
// only a hook's client spool holds (a prompt whose live send failed), and the record is cleared
// after that drain. Every row audits the store when this returns, so returning at the marker let
// the audit race the drain (TestFault_FlushWaitsForASpooledPromptsFinalDrain; test/integration's
// awaitSessionEnded waits the same way).
//
// Both marker reads are shared (paths.ReadFileShared). The daemon writes the marker once per session
// end with paths.WriteAtomic and never retries it, and on Windows an ordinary handle held by this
// poll would fail that replace, so the wait would time out on a write its own read prevented
// (test/guards' sharedReaders; the same defect w4-e2eflakes fixed in test/e2e's marker poll).
func flushAndAwaitEnd(t *testing.T, b bundle, p project, sess core.SessionID) bool {
	t.Helper()
	before, _ := paths.ReadFileShared(contract.MarkerPath(p.Root))
	runHook(t, b.Bin, p, []string{"flush"}, sessionEndPayload(t, p.Root, sess))

	ticker := time.NewTicker(daemonPollTick)
	defer ticker.Stop()
	deadline := time.NewTimer(indexBound)
	defer deadline.Stop()
	for {
		if raw, err := paths.ReadFileShared(contract.MarkerPath(p.Root)); err == nil && !bytes.Equal(raw, before) {
			var m struct {
				Session core.SessionID `json:"session"`
			}
			if json.Unmarshal(raw, &m) == nil && m.Session == sess {
				if sr, err := daemon.LoadSessionRecovery(p.Root); err == nil {
					if _, pending := sr.Sessions[sess]; !pending {
						return true
					}
				}
			}
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			return false
		}
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
// host sends (tool_input, tool_response) are free-form JSON that Event's typed shape would flatten,
// and because two rows here deliberately send a `source` value the parser may not know.
//
// `cwd` is always a NATIVE absolute path: a POSIX-flavoured cwd on Windows resolves to a directory
// that is not there, and the hook then answers correctly while observing nothing — a green test that
// asserted nothing (test/e2e/hooks_test.go makes the same point).

func marshalPayload(t *testing.T, doc map[string]any) []byte {
	t.Helper()
	root, _ := doc["cwd"].(string)
	if !filepath.IsAbs(root) {
		t.Fatalf("fault: a hook payload cwd %q must be a native absolute path", root)
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("fault: marshalling a hook payload: %v", err)
	}
	return b
}

// sessionStartPayload builds a SessionStart with the caller's own `source`, which is the whole
// lifecycle axis: startup, resume, fork, clear and compact are five different statements about
// what the host just did, and the observer branches on them (internal/observer/session.go).
func sessionStartPayload(t *testing.T, root string, sess core.SessionID, source string) []byte {
	t.Helper()
	return marshalPayload(t, map[string]any{
		"hook_event_name": "SessionStart", "session_id": string(sess), "cwd": root, "source": source,
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

func promptPayload(t *testing.T, root string, sess core.SessionID, prompt string) []byte {
	t.Helper()
	return marshalPayload(t, map[string]any{
		"hook_event_name": "UserPromptSubmit", "session_id": string(sess), "cwd": root, "prompt": prompt,
	})
}

// preCompactPayload builds a PreCompact with the caller's own `trigger`: "manual" when the user
// asked for the compaction and "auto" when the host decided, which §2.3 keeps apart.
func preCompactPayload(t *testing.T, root string, sess core.SessionID, trigger string) []byte {
	t.Helper()
	return marshalPayload(t, map[string]any{
		"hook_event_name": "PreCompact", "session_id": string(sess), "cwd": root, "trigger": trigger,
	})
}

func stopPayload(t *testing.T, root string, sess core.SessionID) []byte {
	t.Helper()
	return marshalPayload(t, map[string]any{
		"hook_event_name": "Stop", "session_id": string(sess), "cwd": root, "stop_hook_active": true,
	})
}

func subagentStopPayload(t *testing.T, root string, sess core.SessionID) []byte {
	t.Helper()
	return marshalPayload(t, map[string]any{
		"hook_event_name": "SubagentStop", "session_id": string(sess), "cwd": root, "stop_hook_active": true,
	})
}

func sessionEndPayload(t *testing.T, root string, sess core.SessionID) []byte {
	t.Helper()
	return marshalPayload(t, map[string]any{
		"hook_event_name": "SessionEnd", "session_id": string(sess), "cwd": root,
	})
}

// sessionID derives a stable, legal session identifier from a case name, so a record and the
// session it drove can be joined by eye in a log.
func sessionID(name string) core.SessionID {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '-'
		}
	}, name)
	return core.SessionID("sess-fault-" + clean)
}

// toolUseID derives a host-shaped tool_use_id from a case name and an ordinal.
func toolUseID(name string, n int) string {
	return fmt.Sprintf("toolu_fault_%s_%02d", strings.ReplaceAll(name, "-", "_"), n)
}

// ---------------------------------------------------------------------------
// Project fixtures
// ---------------------------------------------------------------------------

// fixturePrefix is the name every temporary directory this package creates begins with.
//
// It is what makes "a daemon this package started" decidable from a path, and it is the ONLY thing
// that authorizes a kill. This machine is shared with other sessions running their own suites; a
// daemon whose lock does not live under a directory named with this prefix belongs to one of them
// and is never signalled, only logged. A pid alone is never enough.
const fixturePrefix = "qompack-fault-"

// project is one disposable project root and the environment a spawned qompack process needs to
// stay inside it.
type project struct {
	Root string
	Home string
	Env  map[string]string
}

// EnvWith returns this project's environment with extra layered on top, without mutating either.
// Every fault row needs it: the injection switch is read once per process from the environment, so a row
// that injects a site has to hand a DIFFERENT map to one invocation and the plain one to the next.
func (p project) EnvWith(extra map[string]string) map[string]string {
	if len(extra) == 0 {
		return p.Env
	}
	out := make(map[string]string, len(p.Env)+len(extra))
	for k, v := range p.Env {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// tempBase returns a disposable enclosing directory with the permission-resetting cleanup registered
// BEFORE anything is created inside it, so the reset always runs first.
//
// It is os.MkdirTemp rather than t.TempDir for test/canary's reason: t.TempDir fails the test if its
// cleanup cannot remove the tree, and on Windows a daemon that has not finished unwinding makes that
// a certainty rather than a risk. Removal here is best-effort.
func tempBase(t *testing.T) string {
	t.Helper()
	base, err := os.MkdirTemp("", fixturePrefix)
	if err != nil {
		t.Fatalf("fault: creating a temporary directory: %v", err)
	}
	t.Cleanup(func() {
		resetPermissionsForCleanup(base)
		_ = os.RemoveAll(paths.Long(base))
	})
	return base
}

// newProjectAt creates a project rooted at exactly the path the caller names, with a sibling HOME.
// The `.git` marker stops paths.Resolve's upward walk at this root rather than letting it escape
// into whatever encloses the OS temp directory.
func newProjectAt(t *testing.T, base, rootName string) project {
	t.Helper()

	root := filepath.Join(base, rootName)
	home := filepath.Join(base, "home")
	for _, d := range []string{root, home, filepath.Join(root, ".git")} {
		if err := os.MkdirAll(paths.Long(d), 0o700); err != nil {
			t.Fatalf("fault: creating %s: %v", d, err)
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

// newProject is newProjectAt over a fresh temp base.
func newProject(t *testing.T, rootName string) project {
	t.Helper()
	return newProjectAt(t, tempBase(t), rootName)
}

// writeProjectFile creates one file inside the project, creating its directories.
func writeProjectFile(t *testing.T, p project, rel, content string) {
	t.Helper()
	full := filepath.Join(p.Root, filepath.FromSlash(rel))
	if err := os.MkdirAll(paths.Long(filepath.Dir(full)), 0o700); err != nil {
		t.Fatalf("fault: creating %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(paths.Long(full), []byte(content), 0o600); err != nil {
		t.Fatalf("fault: writing %s: %v", full, err)
	}
}

// resetPermissionsForCleanup best-effort undoes anything a case did to dir's permissions, so the
// enclosing RemoveAll is never blocked by a deny this package installed itself. It is registered
// before the directory is populated, so it always runs first (cloned from test/e2e's
// resetPermissionsForCleanup, which nothing may import).
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

// windowsDenyMask is the icacls right set that makes a directory non-writable while leaving it
// readable, listable and EXECUTABLE. Task 2 established it: the simple `W` right also blocks
// reading (the deny then measures this package's own blindness) and blocks CreateProcess on a
// binary beneath it (a managed install is non-writable and still runnable). The named rights are
// icacls' own spellings: WD write-data/add-file, AD append-data/add-subdirectory, WEA
// write-extended-attributes, WA write-attributes, DE delete, DC delete-child.
const windowsDenyMask = ":(OI)(CI)(WD,AD,WEA,WA,DE,DC)"

// denyWrites makes dir non-writable for the current user while leaving it readable and traversable:
// a real deny-ACE via icacls on Windows (the FILE_ATTRIBUTE_READONLY bit is a near-no-op for
// directories there), plain permission bits elsewhere.
func denyWrites(dir string) error {
	if runtime.GOOS == "windows" {
		u, err := user.Current()
		if err != nil {
			return err
		}
		return exec.Command("icacls", dir, "/deny", u.Username+windowsDenyMask).Run() //nolint:gosec // G204: fixed subcommand over this package's own temp directory
	}
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
const denyProbeName = "qompack-fault-deny-probe.tmp"

// denyBites checks, from this process, that a deny actually took effect — and that it took effect on
// writes ONLY.
//
// A deny that blocks LISTING is a defect in this package's mask, so it fails the test immediately. A
// deny that does not bite at all is a fact about the environment — a process running as root ignores
// POSIX mode bits, a privileged Windows token can ignore a deny ACE — so it is reported to the
// caller, which records a skip. A hard failure there would turn "this host cannot express a
// read-only directory to us" into "the product is broken" (Task 2's denyBites, same reasoning).
func denyBites(t *testing.T, dir string) bool {
	t.Helper()
	if _, err := os.ReadDir(paths.Long(dir)); err != nil {
		t.Fatalf("fault: the deny on %s also blocked listing it (%v); a read-only directory must "+
			"stay readable, or this case measures its own blindness rather than the product", dir, err)
	}
	probe := filepath.Join(dir, denyProbeName)
	if err := os.WriteFile(paths.Long(probe), []byte("probe"), 0o600); err == nil {
		_ = os.Remove(paths.Long(probe))
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// Daemon lifecycle
// ---------------------------------------------------------------------------

const (
	// probeTimeout bounds one reachability dial.
	probeTimeout = 250 * time.Millisecond
	// roundTripDeadline bounds one admin request's connect and ACK.
	roundTripDeadline = 5 * time.Second
	// daemonUpBound is how long a case waits for session-start's daemon to answer a dial. It is far
	// longer than session-start's own wait on purpose: its EnsureRunningUntil has already polled
	// until the hook budget's borrow limit, 8.25 s after the hook began (V6 close-out D21), or for
	// daemon.SpawnPollBound after a spawn that itself ran late, so anything still outstanding is a
	// cold start on a loaded machine.
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
	// killSettleBound is how long killDaemon waits for a killed daemon to stop being alive. A kill
	// is synchronous on both platforms this runs on, so this is a bound on the reporting, not on
	// the mechanism.
	killSettleBound = 20 * time.Second
)

// waitDaemonUp polls root's resolved address until something answers, and reports whether it did.
func waitDaemonUp(t *testing.T, root string) bool {
	t.Helper()
	return waitDaemonUpFor(t, root, daemonUpBound)
}

// absentDaemonBound is how long a case that EXPECTS no daemon waits before saying so. It is short
// on purpose: a session-start that never reached its daemon bootstrap has no cold start to be
// patient about, and waiting the full daemonUpBound would cost a minute per case to establish
// nothing the first few seconds did not.
const absentDaemonBound = 10 * time.Second

// waitDaemonUpFor is waitDaemonUp with the caller's own bound.
func waitDaemonUpFor(t *testing.T, root string, bound time.Duration) bool {
	t.Helper()
	addr, err := ipc.Resolve(root)
	if err != nil {
		t.Fatalf("fault: ipc.Resolve(%s): %v", root, err)
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
				t.Logf("fault: a daemon answered %s after %s", root, time.Since(started).Round(time.Millisecond))
				return true
			}
		case <-deadline.C:
			// Every expiry says what it was waiting for and for how long: a bound that expires
			// silently is barely better than none.
			pid, held := testutil.DaemonHoldingLock(root)
			t.Logf("fault: no daemon answered %s within %s (lock pid %d, held=%v)", root, bound, pid, held)
			return false
		}
	}
}

// faultSpawnLockName is internal/ipc's unexported spawnLockName, respelled as test/e2e respells it:
// the claim a spawner writes in <root>/.qompack/run, carrying its UnixMilli stamp, before it launches
// a detached daemon. That daemon removes it once it listens.
const faultSpawnLockName = "spawn.lock"

// faultSpawnLockStaleAfter is internal/ipc's unexported spawnLockStaleAfter: within it a claim holds
// every other spawner off, session-start's included (ipc.ClaimSpawn, internal/daemon/spawn.go).
const faultSpawnLockStaleAfter = 10 * time.Second

// spawnClaimFresh reports whether root holds a spawn.lock the product still counts as a spawn in
// flight, judged as ipc's readSpawnLock judges it: by its stamp, or by its modification time when
// the stamp does not parse or is dated after now. It reads through paths.ReadFileShared, because the
// daemon deletes this file and a reader without FILE_SHARE_DELETE would make that delete fail on
// Windows.
func spawnClaimFresh(root string) bool {
	path := filepath.Join(paths.Of(root).Run, faultSpawnLockName)
	b, err := paths.ReadFileShared(path)
	if err != nil {
		return false
	}
	if ms, perr := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); perr == nil {
		if age := time.Since(time.UnixMilli(ms)); age >= 0 {
			return age <= faultSpawnLockStaleAfter
		}
	}
	fi, err := os.Stat(paths.Long(path))
	return err == nil && time.Since(fi.ModTime()) <= faultSpawnLockStaleAfter
}

// awaitNoSpawnClaim waits until root holds no fresh spawn claim (spawnClaimFresh): the daemon it
// announced has listened and removed it, or it has lapsed. It is called only while no hook of the
// case is running, and only a spawner makes a claim, so a claim present now is gone within
// faultSpawnLockStaleAfter; the bound adds a tick of polling and the stamp's millisecond to that.
//
// A claim can be an orphan, and two cuts here leave one. A burst hook whose connect missed its
// deadline under load spawns a duplicate daemon, which loses daemon.lock and exits leaving its claim
// behind (D35(a), D61(c)), and a kill of the owner then leaves that claim standing (D73(1)). A hook
// run under the daemon-down fault site claims and then spawns nothing (internal/cli noopSpawn), so
// every TestFault_WriteFailures row leaves one too. Either claim holds off the recovery's
// session-start, which spawns nothing and spools, and a fixture that then waits for a daemon with
// dials only reads the product's recovery as "recording did not resume". A row that sends the next
// hook only once the claim is gone measures the recovery it is about, not that one-claim window.
func awaitNoSpawnClaim(t *testing.T, root string) {
	t.Helper()
	if !spawnClaimFresh(root) {
		return
	}
	started := time.Now()
	t.Logf("fault: a fresh spawn claim stands in %s with no hook running; waiting for its daemon or its lapse", root)
	ticker := time.NewTicker(daemonPollTick)
	defer ticker.Stop()
	deadline := time.NewTimer(faultSpawnLockStaleAfter + daemonPollTick + time.Millisecond)
	defer deadline.Stop()
	for spawnClaimFresh(root) {
		select {
		case <-ticker.C:
		case <-deadline.C:
			// Only a spawner refreshes a claim, so this is a spawner the case did not account for.
			// The row goes on and its verdict says what the next hook met; the log says why.
			t.Logf("fault: a spawn claim in %s stayed fresh past %s with no hook running",
				root, faultSpawnLockStaleAfter)
			return
		}
	}
	t.Logf("fault: the spawn claim in %s was gone after %s", root, time.Since(started).Round(time.Millisecond))
}

// waitIndexed waits until index/tool_use.jsonl names id and reports whether it ever did.
//
// It REPORTS rather than fatals, which is the difference between this and test/security's
// requireIndexed. Half the rows here cut the daemon before it could index anything, and "the event
// never landed" is the measurement rather than a harness failure.
func waitIndexed(t *testing.T, root, id string, bound time.Duration) bool {
	t.Helper()
	path := filepath.Join(paths.Of(root).Index, "tool_use.jsonl")

	ticker := time.NewTicker(daemonPollTick)
	defer ticker.Stop()
	deadline := time.NewTimer(bound)
	defer deadline.Stop()
	for {
		if b, err := paths.ReadFileShared(path); err == nil && bytes.Contains(b, []byte(id)) {
			return true
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			return false
		}
	}
}

// requireIndexed waits for id and fails the case if it never lands. Only the rows that need a
// COMPLETE capture before they cut anything use it; the rows that cut mid-flight use waitIndexed.
func requireIndexed(t *testing.T, root, id string) {
	t.Helper()
	if waitIndexed(t, root, id, indexBound) {
		return
	}
	path := filepath.Join(paths.Of(root).Index, "tool_use.jsonl")
	size := int64(-1)
	if fi, statErr := os.Stat(paths.Long(path)); statErr == nil {
		size = fi.Size()
	}
	pid, held := testutil.DaemonHoldingLock(root)
	t.Fatalf("fault: the observer never indexed %s into %s within %s "+
		"(index size %d bytes, daemon lock pid %d held=%v)", id, path, indexBound, size, pid, held)
}

// shutdownIfReachable dials root's resolved address and, if anything answers or a live process still
// holds the lock, sends admin.shutdown until the daemon is gone.
//
// What "gone" means is testutil.ShutdownDaemonUntilGone's one definition, shared with every other
// shutdown helper under test/: no live process holds the lock, every process seen holding it during
// the call has exited, and no holder went unidentified. This helper keeps only what is this
// package's own: a daemon that is still COMING UP holds the lock while answering no dial at all, so
// liveness of the lock holder — not reachability alone — decides whether there is anything to wait
// for; and a daemon that never goes is terminated, if it serves one of this package's fixtures.
//
// It never signals a process on the ordinary path: shutdown is requested over the daemon's own admin
// channel. terminateOwnDaemon is the last resort and it is guarded to this package's own fixtures.
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
		Tick: daemonPollTick, Bound: daemonDownBound, RoundTrip: roundTripDeadline,
	})
	if out.Gone {
		return
	}
	t.Logf("fault: %s", out.Describe(daemon.LockPath(root), daemonDownBound))
	terminateOwnDaemon(t, root, out.LockPID)
}

// killDaemon is this package's daemon-side cut: it takes the pid out of root's daemon.lock and kills
// it, without a shutdown request and without waiting for an unwind.
//
// This is the ONLY forced daemon failure available. The injection switch never reaches a daemon —
// internal/daemon/spawn.go's buildSpawnEnv strips it case-insensitively, and
// test/guards/faultenv_test.go forbids adding a second seam — so a cut that has to land inside the
// daemon is either this or a mutation of files the daemon already wrote.
//
// The guard is the whole of its safety, and it is the process-safety rule this task runs under: this
// machine hosts other sessions' suites, and a daemon whose project is not under a `qompack-fault-`
// directory THIS package created is one of theirs. It is logged and left strictly alone. Nothing
// here ever signals a pid it did not find in one of its own fixtures' lock files.
//
// It returns whether a daemon was actually killed, so a row can record "there was nothing to cut"
// as the observation it is rather than as a silent pass.
func killDaemon(t *testing.T, root string) bool {
	t.Helper()
	pid, held := testutil.DaemonHoldingLock(root)
	if !held || pid <= 0 {
		t.Logf("fault: no live daemon held %s; there was nothing to cut", daemon.LockPath(root))
		return false
	}
	if !ownFixture(root) {
		t.Fatalf("fault: refusing to kill daemon pid %d: %s is not one of this package's %s fixtures",
			pid, root, fixturePrefix)
	}
	if pid == os.Getpid() {
		t.Fatalf("fault: the lock on %s names this test process", root)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("fault: could not address daemon pid %d for %s: %v", pid, root, err)
	}
	if err := proc.Kill(); err != nil {
		t.Logf("fault: could not kill daemon pid %d for %s: %v (it may already have exited)", pid, root, err)
	}
	if !waitProcessGone(pid, killSettleBound) {
		t.Errorf("fault: daemon pid %d was still alive %s after Kill", pid, killSettleBound)
		return false
	}
	t.Logf("fault: killed daemon pid %d serving the fixture %s", pid, root)
	return true
}

// waitProcessGone polls until pid is no longer alive, and reports whether it got there.
func waitProcessGone(pid int, bound time.Duration) bool {
	ticker := time.NewTicker(daemonPollTick)
	defer ticker.Stop()
	deadline := time.NewTimer(bound)
	defer deadline.Stop()
	for {
		if !testutil.ProcessAlive(pid) {
			return true
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			return !testutil.ProcessAlive(pid)
		}
	}
}

// ownFixture reports whether root lies under a directory this package named. It is the predicate
// every kill in this file is gated on.
func ownFixture(root string) bool {
	return strings.Contains(filepath.ToSlash(root), fixturePrefix)
}

// terminateOwnDaemon is the last resort when a daemon will not answer admin.shutdown: kill the
// process the lock names, but ONLY when the project it is serving is one of this package's own
// fixtures. A cleanup that gives up quietly is how a daemon comes to outlive the suite by an hour.
func terminateOwnDaemon(t *testing.T, root string, pid int) {
	t.Helper()
	if pid <= 0 || pid == os.Getpid() || !testutil.ProcessAlive(pid) {
		return
	}
	if !ownFixture(root) {
		t.Logf("fault: leaving daemon pid %d alone: %s is not one of this package's fixtures", pid, root)
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		t.Logf("fault: could not address daemon pid %d for %s: %v", pid, root, err)
		return
	}
	if err := proc.Kill(); err != nil {
		t.Logf("fault: could not terminate daemon pid %d for %s: %v", pid, root, err)
		return
	}
	t.Logf("fault: terminated daemon pid %d, which would not answer admin.shutdown for the "+
		"fixture project %s", pid, root)
}

// requireNoOrphan asserts, at the end of a case, that nothing this package started is still holding
// the project: no live lock holder and no reachable address. It is the rule "every scenario tears
// its daemon down (lock gone, pid dead)" made checkable rather than assumed.
func requireNoOrphan(t *testing.T, root string) {
	t.Helper()
	if pid, held := testutil.DaemonHoldingLock(root); held {
		t.Errorf("fault: daemon pid %d still holds %s after the case finished", pid, daemon.LockPath(root))
	}
	if addr, err := ipc.Resolve(root); err == nil && ipc.Probe(addr, probeTimeout) {
		t.Errorf("fault: something still answers %s after the case finished", root)
	}
}

// ---------------------------------------------------------------------------
// Sessions: seeding state and recovering from a cut
// ---------------------------------------------------------------------------

// seedSession drives one complete, ordinary session through the installed binary: session-start
// (which brings the detached daemon up), a prompt, two tool observations, and a flush. It is what
// puts real objects, real index lines, a real capture sidecar and a real spool behind every cut.
func seedSession(t *testing.T, b bundle, p project, sess core.SessionID) {
	t.Helper()
	name := strings.TrimPrefix(string(sess), "sess-fault-")

	writeProjectFile(t, p, "src/alpha.ts", seedContent("alpha", 48))
	writeProjectFile(t, p, "src/beta.ts", seedContent("beta", 64))

	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, sess, "startup"))
	if !waitDaemonUp(t, p.Root) {
		t.Fatalf("fault: session-start did not bring a daemon up for %s", p.Root)
	}
	runHook(t, b.Bin, p, []string{"observe", "prompt"},
		promptPayload(t, p.Root, sess, "seed the state this row is going to cut"))

	first := toolUseID(name, 1)
	runHook(t, b.Bin, p, []string{"observe", "tool"},
		readToolPayload(t, p.Root, sess, first, "src/alpha.ts", seedContent("alpha", 48)))
	requireIndexed(t, p.Root, first)

	second := toolUseID(name, 2)
	runHook(t, b.Bin, p, []string{"observe", "tool"},
		readToolPayload(t, p.Root, sess, second, "src/beta.ts", seedContent("beta", 64)))
	requireIndexed(t, p.Root, second)

	// The flush's end runs in the daemon after the hook answers (C1.15); the seed is complete, and
	// the baseline the caller takes next is comparable, only once that end has finished.
	runFlush(t, b, p, sess)
	// The daemon is deliberately LEFT UP. The caller takes its pre-cut degradation baseline while
	// something can still answer `status --json` — `StatusReport.Snapshot` is the daemon's, and a
	// baseline taken with the daemon down would not be comparable with the post-recovery reading,
	// which R4-1 requires to be taken with the recovery daemon up. The caller stops it before the
	// cut, because every cut mutates files the daemon owns.
}

// recoverSession is the recovery half of every boundary row: session-start, a tool event and a
// flush, with the daemon restarted. It reports whether the recovery session's own observation was
// indexed, which is the "did the product go on recording" half of `recovered`.
//
// Like seedSession it leaves the daemon UP, for the same reason: the degradation reading that
// decides this row's outcome has to be taken while the reporting surface can answer.
func recoverSession(t *testing.T, b bundle, p project, sess core.SessionID) bool {
	t.Helper()
	name := strings.TrimPrefix(string(sess), "sess-fault-")

	writeProjectFile(t, p, "src/gamma.ts", seedContent("gamma", 40))
	// The cut may have left a fresh spawn claim with no daemon behind it (awaitNoSpawnClaim): the
	// recovery's session-start is sent once it is gone, so that it can spawn.
	awaitNoSpawnClaim(t, p.Root)
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, sess, "startup"))
	up := waitDaemonUp(t, p.Root)

	id := toolUseID(name, 1)
	runHook(t, b.Bin, p, []string{"observe", "tool"},
		readToolPayload(t, p.Root, sess, id, "src/gamma.ts", seedContent("gamma", 40)))
	indexed := up && waitIndexed(t, p.Root, id, indexBound)

	// The recovery session's end runs in the daemon after the hook answers (C1.15), and what it
	// does — its GC over the cut state among them — is part of what the degradation reading taken
	// next must see. A cut can leave the product unable to take the flush at all (a refused
	// configuration, an unavailable delivery identity), and then there is no end to wait for: that
	// is part of what the row measures, so it is logged, not failed.
	if !flushAndAwaitEnd(t, b, p, sess) {
		t.Logf("fault: the recovery session %s was not ended within %s of its flush hook; the "+
			"degradation reading is taken without that end", sess, indexBound)
	}
	return indexed
}

// seedContent builds deterministic, compressible-but-not-degenerate file content. Every row needs
// bytes big enough to chunk and stable enough that two rows produce comparable stores.
func seedContent(tag string, lines int) string {
	var b strings.Builder
	for i := range lines {
		fmt.Fprintf(&b, "export const %s_%03d = { id: %d, note: \"a line of %s that exists to be chunked\" };\n",
			tag, i, i, tag)
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// The consistency audit
// ---------------------------------------------------------------------------

// The audit is this package's answer to SP17-M7-04's "no newly dangling pointer". It walks
// `.qompack/` after a recovery and names every reference that does not resolve.
//
// `qompack fsck` does not exist yet — Task 5 builds it — so this is a TEST HELPER, and it is
// deliberately narrower than fsck will be: it reports, it never repairs, and it never quarantines.
// Where it reads objects it goes through store.Store's own accessors, whose verify-on-read may
// quarantine a damaged object as a side effect; that is the product's §12.3 behaviour and the audit
// records it rather than routing around it.
//
// The classes below are the checks the brief enumerates. Each says, in its own comment, whether
// Task 5's fsck should absorb it.

// refKind names one class of reference the audit resolves.
type refKind string

const (
	// refToolUseRoot: an index/tool_use.jsonl record whose Root does not resolve. fsck: ABSORB.
	refToolUseRoot refKind = "tool_use_root"
	// refRootChunk: an index/roots.jsonl content record naming a chunk that is not held. fsck: ABSORB.
	refRootChunk refKind = "root_chunk"
	// refDeltaBase / refDeltaOrig: a v2 delta pointer that does not resolve. fsck: ABSORB.
	refDeltaBase refKind = "delta_base"
	refDeltaOrig refKind = "delta_orig"
	// refIndexBadLine: a line in roots.jsonl or tool_use.jsonl that does not parse. store.loadRoots
	// collapses these into a counter; fsck needs its own line-by-line scan. fsck: ABSORB.
	refIndexBadLine refKind = "index_bad_line"
	// refCaptureUnpublished: a capture sidecar at stage 1 only. fsck: ABSORB (report, never repair —
	// sidecars are evidence).
	refCaptureUnpublished refKind = "capture_unpublished"
	// refCaptureBytesAbsent: a published sidecar whose root does not resolve. fsck: ABSORB.
	refCaptureBytesAbsent refKind = "capture_bytes_absent"
	// refManifestArtifact: a MANIFEST line whose artifact is missing or mismatched. fsck: ABSORB
	// (this is Reader.Verify, which the interface already names as fsck's).
	refManifestArtifact refKind = "manifest_artifact"
	// refOrphanArtifact: a checkpoint artifact with no MANIFEST line. fsck: ABSORB (re-hash and
	// REPORT; appending is a repair and paths.AppendManifest is the only legal writer).
	refOrphanArtifact refKind = "orphan_artifact"
	// refCheckpointPointer: a checkpoint pointer into the store that does not resolve, by
	// finalize.go's root-or-chunks rule. fsck: ABSORB.
	refCheckpointPointer refKind = "checkpoint_pointer"
	// refRetentionRoot: a retention-roots line naming a hash that is not held. fsck: ABSORB, with
	// the distinction gcrun.go draws: an unreadable in-process SOURCE stops the pass
	// (errRetentionRootsUnavailable), while an unparseable FILE LINE does not — it retains everything
	// on the line under the blanket rollback class instead.
	refRetentionRoot refKind = "retention_root"
	// refDrainGap: state/drain.json disagrees with the spool. fsck: PARTIAL — the vocabulary is
	// daemon's (DrainGapKind) and doctor is the better home for "unreplayed spool bytes".
	refDrainGap refKind = "drain_gap"
	// refJournalUnreadable: a delivery lease/ack journal that will not load. fsck: ABSORB.
	refJournalUnreadable refKind = "journal_unreadable"
)

// danglingRef is one reference that does not resolve.
type danglingRef struct {
	Kind refKind `json:"kind"`
	// ID identifies the reference well enough to be asked about again: a tool_use_id, a hash, a
	// file name, a spool base name.
	ID     string `json:"id"`
	Detail string `json:"detail"`
	// Reported says the AUDIT could name this reference as one the product already accounts for —
	// a GC tombstone over the root, a quarantined object, a checkpoint DropEntry. It is the
	// brief's "every remaining dangling reference is one the audit can name as reported".
	Reported bool `json:"reported"`
}

// key is the identity two audits are compared on, so a reference that was already dangling before a
// cut is not counted as one the cut introduced.
func (d danglingRef) key() string { return string(d.Kind) + "|" + d.ID }

// auditResult is one walk of `.qompack/`.
type auditResult struct {
	// Dangling is every unreported reference that does not resolve.
	Dangling []danglingRef `json:"dangling"`
	// Reported is every reference that does not resolve and that the audit could name as accounted
	// for. It is kept apart because the acceptance row tolerates exactly these.
	Reported []danglingRef `json:"reported"`
	// Notes are checks that could not run, with why. A check that silently did not run is a check
	// nobody may cite.
	Notes []string `json:"notes,omitempty"`
	// Counts are the population each check scanned, so a zero-dangling result over a zero-sized
	// store is distinguishable from a real pass.
	ToolUses    int `json:"tool_uses"`
	Roots       int `json:"roots"`
	Sidecars    int `json:"sidecars"`
	Checkpoints int `json:"checkpoints"`
}

// String renders an audit compactly for a record's Detail field.
func (a auditResult) String() string {
	return fmt.Sprintf("{tool_uses:%d roots:%d sidecars:%d checkpoints:%d dangling:%d reported:%d%s}",
		a.ToolUses, a.Roots, a.Sidecars, a.Checkpoints, len(a.Dangling), len(a.Reported),
		notesSuffix(a.Notes))
}

func notesSuffix(notes []string) string {
	if len(notes) == 0 {
		return ""
	}
	return " notes:" + strings.Join(notes, ";")
}

// describe renders the dangling references themselves, for a failing row's message.
func describeRefs(refs []danglingRef) string {
	if len(refs) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(refs))
	for _, r := range refs {
		parts = append(parts, fmt.Sprintf("%s(%s): %s", r.Kind, r.ID, r.Detail))
	}
	return strings.Join(parts, "; ")
}

// indexGrowth renders which of an audit's four populations grew between two walks, or "" when none
// did. It exists so a row can say what a delivery caused to be WRITTEN instead of asserting what it
// did not write: "produced no tool_use record" was hard-coded in one row beside a record showing
// tool_uses, roots and sidecars all rising by one.
func indexGrowth(before, after auditResult) string {
	var parts []string
	for _, g := range []struct {
		label    string
		from, to int
	}{
		{"tool_use records", before.ToolUses, after.ToolUses},
		{"roots", before.Roots, after.Roots},
		{"capture sidecars", before.Sidecars, after.Sidecars},
		{"checkpoint artifacts", before.Checkpoints, after.Checkpoints},
	} {
		if g.to > g.from {
			parts = append(parts, fmt.Sprintf("%s %d→%d", g.label, g.from, g.to))
		}
	}
	return strings.Join(parts, ", ")
}

// newlyDangling returns the references dangling after a cut that were not dangling before it. The
// acceptance row is about the DELTA — "no NEWLY dangling pointer" — and a store that arrives with a
// pre-existing gap must not make every later row read as a failure.
func newlyDangling(before, after auditResult) []danglingRef {
	return newRefsIn(before.Dangling, after.Dangling)
}

// newlyReported is newlyDangling over the references the audit CAN name as accounted for. The brief
// tolerates exactly these — "or every remaining dangling reference is one the audit can name as
// reported" — so a cut that produced only these left an incompleteness that is explicit rather than
// silent, and that is a different answer from "it recovered".
func newlyReported(before, after auditResult) []danglingRef {
	return newRefsIn(before.Reported, after.Reported)
}

// newRefsIn returns the members of after whose identity does not appear in before.
func newRefsIn(before, after []danglingRef) []danglingRef {
	had := make(map[string]bool, len(before))
	for _, d := range before {
		had[d.key()] = true
	}
	var out []danglingRef
	for _, d := range after {
		if !had[d.key()] {
			out = append(out, d)
		}
	}
	return out
}

// auditBound bounds the whole walk. It reads and re-hashes every object a reference names, so on a
// seeded fixture it is milliseconds and on a pathological one it is still finite.
const auditBound = 2 * time.Minute

// auditProject walks root's `.qompack/` and reports every reference that does not resolve.
//
// It opens its own store.Store, read-only in effect. Most call sites run with the daemon already
// stopped — the post-recovery question — but TestFault_Lifecycle audits beside its one live daemon
// between rows, so a walk may race a publication. auditRetentionRoots reads each claim before the
// evidence it names, in the producer's own order, so a capture published mid-walk is never a false
// dangling reference; auditRoots and auditToolUses read their index lines before resolving what they
// name. Two walks are not safe against a publication in flight, and the matrix's rows settle their
// deliveries (waitIndexed, runFlush) before auditing for that reason: the store's roots are the ones
// loaded at Open, and the orphan-checkpoint scan lists artifacts after reading the manifest. A store
// that cannot be opened at all is a Note rather than a fatal: half the rows here damage the very
// files Open reads, and "the store will not open" is a finding, not a harness error.
func auditProject(t *testing.T, root string) auditResult {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), auditBound)
	defer cancel()

	var res auditResult
	l := paths.Of(root)

	s, err := store.Open(root, config.Defaults(), store.Deps{})
	if err != nil {
		res.Notes = append(res.Notes, "store.Open refused: "+err.Error())
		// Every remaining check needs the store to resolve a hash, so the rest of the audit is
		// about what can be read from the files alone.
		auditDrain(&res, root)
		auditJournals(&res, l)
		return res
	}
	defer func() { _ = s.Close() }()

	tombstoned := auditRoots(ctx, &res, s, root)
	auditToolUses(ctx, &res, s, root, tombstoned)
	auditSidecars(ctx, &res, s, root)
	auditCheckpoints(ctx, t, &res, s, root)
	auditRetentionRoots(ctx, &res, s, root)
	auditDrain(&res, root)
	auditJournals(&res, l)
	return res
}

// rootLine is the subset of index/roots.jsonl's wire shape the audit resolves. The full shape lives
// on store.rootWire; reading it here rather than through the store is deliberate, because
// store.loadRoots collapses every malformed line into a counter and the audit has to NAME them.
type rootLine struct {
	V      int    `json:"v"`
	Op     string `json:"op"`
	Root   string `json:"root"`
	Tool   string `json:"tool"`
	Chunks []struct {
		H string `json:"h"`
		N int64  `json:"n"`
	} `json:"chunks"`
	Deltas string `json:"deltas"`
	Base   string `json:"base"`
	Orig   string `json:"orig"`
	Eph    bool   `json:"eph"`
}

// auditRoots scans index/roots.jsonl: every content record's chunks must be held, every v2 delta
// pointer must resolve, and every line must parse. It returns the set of roots a `gc` tombstone
// covers, which is what makes a collected root a REPORTED absence rather than a dangling one.
func auditRoots(ctx context.Context, res *auditResult, s store.Store, root string) map[string]bool {
	tombstoned := map[string]bool{}
	path := filepath.Join(paths.Of(root).Index, "roots.jsonl")

	lines, err := readJSONLines(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			res.Notes = append(res.Notes, "roots.jsonl unreadable: "+err.Error())
		}
		return tombstoned
	}

	// Two passes: tombstones first, because a `gc` record can follow the content record it covers.
	parsed := make([]rootLine, 0, len(lines))
	for i, raw := range lines {
		var rl rootLine
		if err := json.Unmarshal(raw, &rl); err != nil {
			res.Dangling = append(res.Dangling, danglingRef{
				Kind: refIndexBadLine, ID: fmt.Sprintf("roots.jsonl:%d", i+1),
				Detail: "line does not parse as a roots record: " + err.Error(),
			})
			continue
		}
		if rl.Op == "gc" {
			tombstoned[rl.Root] = true
		}
		parsed = append(parsed, rl)
	}

	for _, rl := range parsed {
		if rl.Op == "gc" {
			continue
		}
		res.Roots++
		for _, c := range rl.Chunks {
			if hashHeld(ctx, s, root, c.H) {
				continue
			}
			res.append(danglingRef{
				Kind: refRootChunk, ID: shortHash(rl.Root) + "/" + shortHash(c.H),
				Detail: fmt.Sprintf("root %s names chunk %s, which the object store does not hold",
					shortHash(rl.Root), shortHash(c.H)),
				Reported: tombstoned[rl.Root],
			})
		}
		for kind, ptr := range map[refKind]string{refDeltaBase: rl.Base, refDeltaOrig: rl.Orig} {
			if ptr == "" || hashHeld(ctx, s, root, ptr) {
				continue
			}
			res.append(danglingRef{
				Kind: kind, ID: shortHash(rl.Root) + "->" + shortHash(ptr),
				Detail: fmt.Sprintf("delta pointer %s of root %s does not resolve",
					shortHash(ptr), shortHash(rl.Root)),
				Reported: tombstoned[rl.Root] || tombstoned[ptr],
			})
		}
	}
	return tombstoned
}

// toolUseLine is the subset of index/tool_use.jsonl's frozen wire shape the audit resolves.
type toolUseLine struct {
	ID     string `json:"id"`
	Root   string `json:"root"`
	Status string `json:"status"`
}

// auditToolUses resolves every tool_use record's Root by finalize.go's OWN rule — the object is
// held, or the root resolves and every chunk it names is held — because that is exactly what
// `expand(hash)` will need. Asking store.Has alone would drop the whole tier as unresolvable
// (V5-VERIFY §4.8); asking GetRoot alone would call a root whose chunks were collected resolvable.
func auditToolUses(ctx context.Context, res *auditResult, s store.Store, root string, tombstoned map[string]bool) {
	path := filepath.Join(paths.Of(root).Index, "tool_use.jsonl")
	lines, err := readJSONLines(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			res.Notes = append(res.Notes, "tool_use.jsonl unreadable: "+err.Error())
		}
		return
	}
	for i, raw := range lines {
		var tu toolUseLine
		if err := json.Unmarshal(raw, &tu); err != nil {
			res.Dangling = append(res.Dangling, danglingRef{
				Kind: refIndexBadLine, ID: fmt.Sprintf("tool_use.jsonl:%d", i+1),
				Detail: "line does not parse as a tool_use record: " + err.Error(),
			})
			continue
		}
		res.ToolUses++
		if tu.Root == "" || isZeroHash(tu.Root) {
			continue
		}
		if hashHeld(ctx, s, root, tu.Root) {
			continue
		}
		res.append(danglingRef{
			Kind: refToolUseRoot, ID: tu.ID,
			Detail: fmt.Sprintf("tool_use %s points at root %s, which cannot be materialized",
				tu.ID, shortHash(tu.Root)),
			Reported: tombstoned[tu.Root],
		})
	}
}

// append files a reference under Dangling or Reported.
func (a *auditResult) append(d danglingRef) {
	if d.Reported {
		a.Reported = append(a.Reported, d)
		return
	}
	a.Dangling = append(a.Dangling, d)
}

// hashHeld reports whether h can still be materialized: the object's bytes are ON DISK, or the root
// resolves and every chunk it lists has its bytes on disk.
//
// It resolves on disk rather than through store.Has, and that is the whole correctness of the audit
// rather than a preference. FSStore.Has (internal/store/read.go:65-75) answers from an in-memory
// chunkSet that Open builds from index/roots.jsonl alone (roots.go:318-323) and never stats a file;
// an object deleted from objects/ while its index line survives is exactly the state every row here
// cuts, and Has reports it present. The first version of this audit used Has and reported
// `dangling 0 -> 0` for a row that had just removed an object file — with every object under
// .qompack/objects/ deleted it would have reported a clean store.
//
// internal/checkpoint/finalize.go's toolResultResolvable HAD the same blindness behind a comment
// that claimed resolvability, and it decides whether a checkpoint keeps a pointer. That was F4-8,
// and the fix gave internal/store an exported store.ObjectPresence.ObjectOnDisk for the question
// this function asks with its own Lstat; the checkpoint decides with that now. Has is unchanged and
// is still the index's belief, which is why the audit here still does not use it.
func hashHeld(ctx context.Context, s store.Store, root, h string) bool {
	parsed, err := core.ParseHash(h)
	if err != nil {
		return false
	}
	if objectOnDisk(root, parsed) {
		return true
	}
	r, err := s.GetRoot(ctx, parsed)
	if err != nil {
		return false
	}
	for _, c := range r.Chunks {
		if !objectOnDisk(root, c.Hash) {
			return false
		}
	}
	return true
}

// objectOnDisk reports whether the object file backing h exists and is a regular file, trying both
// spellings store's own objectCandidates tries: the compressed `.zst` and the bare one
// (internal/store/objects.go:64-88, the fanout-2 layout objects/<hx[0:2]>/<hx[2:4]>/<hx>).
//
// A Lstat rather than a read: the question is whether the bytes are there at all, and reading them
// through the store would quarantine a damaged object as a side effect, which is the product's
// §12.3 behaviour and not something a read-only audit should trigger on its own.
func objectOnDisk(root string, h core.Hash) bool {
	p, ok := objectFilePath(root, h)
	if !ok {
		return false
	}
	fi, err := os.Lstat(paths.Long(p))
	return err == nil && fi.Mode().IsRegular()
}

// objectFilePath returns the path of the object file backing h, and whether one is there.
func objectFilePath(root string, h core.Hash) (string, bool) {
	hx := strings.TrimPrefix(h.String(), "sha256:")
	if len(hx) < 4 {
		return "", false
	}
	dir := filepath.Join(paths.Of(root).Objects, hx[0:2], hx[2:4])
	for _, name := range []string{hx + objectSuffix, hx} {
		p := filepath.Join(dir, name)
		if fi, err := os.Lstat(paths.Long(p)); err == nil && fi.Mode().IsRegular() {
			return p, true
		}
	}
	return "", false
}

// objectSuffix is the extension a compressed object carries (internal/store/objects.go:23).
const objectSuffix = ".zst"

// isZeroHash reports whether h is the empty/zero hash a record carries when it names nothing.
func isZeroHash(h string) bool {
	trimmed := strings.TrimPrefix(h, "sha256:")
	return trimmed == "" || strings.Trim(trimmed, "0") == ""
}

// shortHash renders a hash briefly enough to read in a failure message.
func shortHash(h string) string {
	trimmed := strings.TrimPrefix(h, "sha256:")
	if len(trimmed) > 12 {
		return trimmed[:12]
	}
	if trimmed == "" {
		return "<empty>"
	}
	return trimmed
}

// readJSONLines returns every non-empty line of a JSONL file. It uses paths.ReadFileShared so a
// daemon that is still unwinding cannot make the read fail on Windows.
func readJSONLines(path string) ([][]byte, error) {
	b, err := paths.ReadFileShared(path)
	if err != nil {
		return nil, err
	}
	var out [][]byte
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		out = append(out, append([]byte(nil), line...))
	}
	if err := sc.Err(); err != nil {
		return out, err
	}
	return out, nil
}

// sidecarLine is the subset of a capture sidecar this audit reads. The full shape is
// store.CaptureSidecar; it is read from the file rather than through ReadCaptureSidecar because the
// audit must tolerate a sidecar that a cut left unparseable.
type sidecarLine struct {
	ObservationID string `json:"observation_id"`
	Op            string `json:"op"`
	ToolUseID     string `json:"tool_use_id"`
	Root          string `json:"root"`
	Published     bool   `json:"published"`
	Outcome       string `json:"outcome"`
	BytesHash     string `json:"bytes_hash"`
}

// auditSidecars walks records/captures/** for the two states a cut leaves behind: a sidecar written
// at stage 1 with no reference joined to it yet (`published:false` — the exact state a crash between
// publication order's first two stages leaves, and store/capture_sidecar.go calls it "a REPORTABLE
// GAP, not a repair"), and a published sidecar whose root no longer resolves.
//
// Sidecars are EVIDENCE: nothing here or in fsck may sweep them, and store's own GC deliberately
// keeps them outside objects/ so a collection cannot reach them.
func auditSidecars(ctx context.Context, res *auditResult, s store.Store, root string) {
	dir := filepath.Join(paths.Of(root).Records, "captures")
	_ = filepath.WalkDir(paths.Long(dir), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			return nil //nolint:nilerr // a vanished entry is an observation, not a failure
		}
		res.Sidecars++
		raw, readErr := paths.ReadFileShared(p)
		if readErr != nil {
			res.Dangling = append(res.Dangling, danglingRef{
				Kind: refCaptureBytesAbsent, ID: d.Name(),
				Detail: "capture sidecar unreadable: " + readErr.Error(),
			})
			return nil
		}
		var sc sidecarLine
		if jsonErr := json.Unmarshal(raw, &sc); jsonErr != nil {
			res.Dangling = append(res.Dangling, danglingRef{
				Kind: refCaptureBytesAbsent, ID: d.Name(),
				Detail: "capture sidecar does not parse: " + jsonErr.Error(),
			})
			return nil
		}
		// `published:false` on its own is NOT a gap, and treating it as one was this audit's first
		// calibration error: it fired two or three times on every ORDINARY turn, because a capture
		// whose outcome is not `ok` retained no admitted bytes and so never had a reference for
		// LinkCaptureReference to join. The sidecar IS the record of that, which is why sidecars
		// live outside objects/ where GC cannot reach them.
		//
		// The SECOND calibration error was the fix for the first: keying the discriminator on
		// `Root` non-zero. `Root` is written only by LinkCaptureReference, which sets
		// `Published = true` in the same assignment (internal/store/capture_sidecar.go:247-258);
		// publishCapture and WriteCaptureSidecar never set it. So a genuine stage-1 sidecar carries
		// `published:false` with a ZERO root — and the guard skipped exactly the state it existed
		// to find.
		//
		// The THIRD was keying on BytesHash alone. A PROMPT delivery admits bytes and produces no
		// ToolUseRecord, so the observer has nothing to link (internal/observer/identity.go:127-139
		// links a record, and only a tool observation makes one). Its sidecar is durable,
		// outcome ok, bytes rooted and permanently unpublished BY DESIGN, and it fired on every
		// clean session.
		//
		// What is left is the state store/capture_sidecar.go actually names: a TOOL delivery — an
		// op that does get a reference joined — whose bytes are durable and whose join never
		// happened. Bytes, outcome and op together; any two of the three describe something else.
		if !sc.Published {
			if sc.Op == string(ipc.OpObserveTool) && sc.Outcome == string(core.OutcomeOK) &&
				sc.BytesHash != "" && !isZeroHash(sc.BytesHash) {
				res.Dangling = append(res.Dangling, danglingRef{
					Kind: refCaptureUnpublished, ID: firstNonEmpty(sc.ObservationID, d.Name()),
					Detail: fmt.Sprintf("capture sidecar for a %s delivery is at stage 1 only: outcome "+
						"%q with bytes %s durable and no reference joined to it (root %s)",
						sc.Op, sc.Outcome, shortHash(sc.BytesHash), shortHash(sc.Root)),
				})
			}
			return nil
		}
		if sc.Root != "" && !isZeroHash(sc.Root) && !hashHeld(ctx, s, root, sc.Root) {
			res.Dangling = append(res.Dangling, danglingRef{
				Kind: refCaptureBytesAbsent, ID: firstNonEmpty(sc.ToolUseID, sc.ObservationID, d.Name()),
				Detail: fmt.Sprintf("published capture sidecar names root %s, which does not resolve",
					shortHash(sc.Root)),
			})
		}
		return nil
	})
}

// auditCheckpoints asks internal/checkpoint's own Reader the two questions the interface already
// names as fsck's — Verify re-hashes every manifest entry, and List says what the manifest promised
// — and then adds the two the Reader cannot ask: an ARTIFACT with no manifest line (finalize.go's
// orphan, "qompack fsck reconciles an orphan by re-hashing it"), and every pointer a checkpoint
// carries into the store.
func auditCheckpoints(ctx context.Context, t *testing.T, res *auditResult, s store.Store, root string) {
	t.Helper()
	l := paths.Of(root)

	reader, err := checkpoint.OpenReader(root, logging.Nop(), nil)
	if err != nil {
		res.Notes = append(res.Notes, "checkpoint.OpenReader refused: "+err.Error())
		return
	}

	bad, verifyErr := reader.Verify(ctx)
	if verifyErr != nil {
		res.Notes = append(res.Notes, "checkpoint Verify refused: "+verifyErr.Error())
	}
	for _, seq := range bad {
		res.Dangling = append(res.Dangling, danglingRef{
			Kind: refManifestArtifact, ID: fmt.Sprintf("%04d", int(seq)),
			Detail: "checkpoints/MANIFEST.jsonl records this seq and its artifact is missing or " +
				"does not re-hash to the recorded digest",
		})
	}

	entries, manifestErr := paths.ReadManifest(l)
	if manifestErr != nil {
		res.Notes = append(res.Notes, "checkpoints/MANIFEST.jsonl unreadable: "+manifestErr.Error())
	}
	recorded := make(map[string]bool, len(entries))
	for _, e := range entries {
		recorded[filepath.Base(paths.CheckpointPath(l, e.Seq))] = true
	}
	res.Checkpoints = len(entries)

	// An artifact with no manifest line. finalize.go writes one when CreateNew succeeded and
	// AppendManifest did not; it is REPORTED, never appended to the manifest from here, because
	// paths.AppendManifest is the only legal writer.
	for _, name := range relativeFiles(l.Checkpoints) {
		if !strings.HasSuffix(name, ".json") || strings.EqualFold(name, "MANIFEST.jsonl") {
			continue
		}
		if recorded[name] {
			continue
		}
		res.Dangling = append(res.Dangling, danglingRef{
			Kind: refOrphanArtifact, ID: name,
			Detail: "checkpoint artifact present with no checkpoints/MANIFEST.jsonl line (orphan)",
		})
	}

	// Every pointer a verifying checkpoint carries into the store.
	refs, listErr := reader.List(ctx)
	if listErr != nil {
		res.Notes = append(res.Notes, "checkpoint List refused: "+listErr.Error())
		return
	}
	for _, ref := range refs {
		cp, _, getErr := reader.Get(ctx, ref.Seq)
		if getErr != nil {
			// A seq the manifest recorded whose artifact will not verify is already reported by
			// Verify above; naming it twice would double-count one defect.
			continue
		}
		auditCheckpointPointers(ctx, res, s, root, cp, ref.Seq)
	}
}

// auditCheckpointPointers resolves one checkpoint's file and tool pointers by finalize.go's
// root-or-chunks rule. A pointer the writer already dropped is not here to be found: DropEntry is
// the explicit report, and this check exists to catch the pointer that survived into the artifact
// and stopped resolving afterwards.
func auditCheckpointPointers(ctx context.Context, res *auditResult, s store.Store, root string, cp checkpoint.Checkpoint, seq core.CheckpointSeq) {
	for _, fp := range cp.Pointers.Files {
		if isZeroHash(fp.Hash.String()) || hashHeld(ctx, s, root, fp.Hash.String()) {
			continue
		}
		res.Dangling = append(res.Dangling, danglingRef{
			Kind: refCheckpointPointer, ID: fmt.Sprintf("%04d/file/%s", int(seq), fp.Path),
			Detail: fmt.Sprintf("checkpoint %04d points at file %s (%s), which does not resolve",
				int(seq), fp.Path, shortHash(fp.Hash.String())),
		})
	}
	for _, tp := range cp.Pointers.Tools {
		if isZeroHash(tp.Hash.String()) || hashHeld(ctx, s, root, tp.Hash.String()) {
			continue
		}
		res.Dangling = append(res.Dangling, danglingRef{
			Kind: refCheckpointPointer, ID: fmt.Sprintf("%04d/tool/%s", int(seq), tp.ToolUseID),
			Detail: fmt.Sprintf("checkpoint %04d points at tool result %s (%s), which does not resolve",
				int(seq), tp.ToolUseID, shortHash(tp.Hash.String())),
		})
	}
}

// retentionLine is one line of state/retention-roots.jsonl (store.RetentionRoot's wire shape).
type retentionLine struct {
	Hash   string `json:"hash"`
	Class  string `json:"class"`
	Reason string `json:"reason"`
}

// auditRetentionAfterRead, when set, runs as each of auditRetentionRoots' two reads of the project
// finishes, with the read's name: retentionReadClaims for the retention-roots file,
// retentionReadEvidence for the capture-sidecar scan. It is nil except inside this package's own
// audit test, which records the order of the reads and publishes a capture after the first one to
// stand in for the live daemon the lifecycle matrix audits beside. The call lives inside each read's
// helper, so the reads cannot be reordered without reordering what the test observes.
var auditRetentionAfterRead func(read string)

// The two reads auditRetentionAfterRead names.
const (
	retentionReadClaims   = "claims"
	retentionReadEvidence = "evidence"
)

// readRetentionClaims reads state/retention-roots.jsonl, the claims auditRetentionRoots checks.
func readRetentionClaims(path string) ([][]byte, error) {
	lines, err := readJSONLines(path)
	if auditRetentionAfterRead != nil {
		auditRetentionAfterRead(retentionReadClaims)
	}
	return lines, err
}

// readRetentionEvidence scans the capture sidecars, the evidence an evidence-class claim names.
func readRetentionEvidence(root string) map[string]bool {
	evidence := sidecarBytesHashes(root)
	if auditRetentionAfterRead != nil {
		auditRetentionAfterRead(retentionReadEvidence)
	}
	return evidence
}

// auditRetentionRoots checks that every hash a producer asked GC to retain is actually still held.
//
// The direction matters: a retention root naming an absent object is not itself data loss — GC
// retains, it does not create — but it IS a reference to nothing, and gcrun.go's own discipline says
// a line it cannot parse is a claim it may not ignore: declaredRetentionLine (gcrun.go:764-780)
// retains everything on that line under the blanket `rollback` class. Collection is NOT stopped by
// a bad file line — only an in-process source's failure does that — so the consequence of a rotted
// file is over-retention and a claim nobody can read, which is worth naming either way.
//
// The claims are read BEFORE the evidence they name, and the order is load-bearing: the lifecycle
// matrix audits beside a live daemon, and a capture is published sidecar first, retention root
// second (store/capture_sidecar.go, writeCaptureSidecar). Read in that order, every claim this walk
// sees was declared after its sidecar was on disk, so a claim that does not resolve is a real one.
// Read the other way round, a capture published between the two reads is a claim without evidence:
// run 36955046276's windows-latest test job reported exactly that for the Stop that
// out_of_order_sessionend sends after SessionEnd, on a store whose stopped-daemon audit was clean
// (TestFault_AuditRetentionRootsReadsTheClaimBeforeItsEvidence).
func auditRetentionRoots(ctx context.Context, res *auditResult, s store.Store, root string) {
	path := store.RetentionRootsPath(root)
	lines, err := readRetentionClaims(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			res.Dangling = append(res.Dangling, danglingRef{
				Kind: refRetentionRoot, ID: filepath.Base(path),
				Detail: "retention-roots.jsonl is unreadable, so every claim it carried is lost to the " +
					"reader that enforces it: " + err.Error(),
			})
		}
		return
	}

	// An `evidence`-class root does NOT name an object. store/capture_sidecar.go registers the
	// sidecar's BytesHash — the digest of the bytes the sidecar itself retains, which live under
	// records/captures/ precisely so that GC cannot reach them (:22-26) — so resolving one against
	// the object store reports every ordinary capture on a healthy project as a dangling reference.
	// That is not a finding, it is the audit asking the wrong store; an evidence root resolves when
	// a sidecar carrying that bytes_hash is on disk.
	evidence := readRetentionEvidence(root)
	for i, raw := range lines {
		var rl retentionLine
		if jsonErr := json.Unmarshal(raw, &rl); jsonErr != nil {
			res.Dangling = append(res.Dangling, danglingRef{
				Kind: refRetentionRoot, ID: fmt.Sprintf("retention-roots.jsonl:%d", i+1),
				Detail: "retention root line does not parse: " + jsonErr.Error(),
			})
			continue
		}
		if rl.Hash == "" || isZeroHash(rl.Hash) || hashHeld(ctx, s, root, rl.Hash) {
			continue
		}
		if rl.Class == string(store.RetentionEvidence) && evidence[rl.Hash] {
			continue
		}
		res.Dangling = append(res.Dangling, danglingRef{
			Kind: refRetentionRoot, ID: shortHash(rl.Hash),
			Detail: fmt.Sprintf("retention root (class %q, %q) names %s, which is not held",
				rl.Class, rl.Reason, shortHash(rl.Hash)),
		})
	}
}

// drainRecord is one entry of state/drain.json (internal/daemon's drainFileRecord wire shape).
type drainRecord struct {
	Size        int64    `json:"size"`
	Offset      int64    `json:"offset"`
	Done        bool     `json:"done"`
	DurableSize bool     `json:"durable_size,omitempty"`
	Pending     []string `json:"pending_blobs,omitempty"`
}

// auditDrain compares state/drain.json with the spool it describes, in internal/daemon's own
// DrainGapKind vocabulary.
//
// Three disagreements are named, and they are exactly the three the drainer itself would report:
//   - progress_unreadable — drain.json will not parse, or records an Offset past its Size, which is
//     a record no pass of the drainer can write (loadState refuses it).
//   - progress_unreadable — a spool file is SHORTER than the durable bound a pass recorded for it,
//     which means bytes that were durable are gone; validateProgress refuses the spool over that.
//   - pending — spool bytes below EOF that no pass has consumed. Unreplayed spool is a RECORDING
//     GAP rather than a dangling pointer, so it is filed as Reported: the product has the bytes and
//     has not lost them, and the next drain replays them.
//
// fsck: PARTIAL. The vocabulary belongs to internal/daemon and `qompack doctor` is the better home
// for "unreplayed spool bytes"; what fsck should absorb is the REFUSAL case — a drain.json that
// disagrees with the spool it describes, because that one wedges recording until someone looks.
func auditDrain(res *auditResult, root string) {
	l := paths.Of(root)
	path := filepath.Join(l.State, "drain.json")

	raw, err := paths.ReadFileShared(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			res.Dangling = append(res.Dangling, danglingRef{
				Kind: refDrainGap, ID: string(daemon.DrainGapProgressUnreadable),
				Detail: "state/drain.json is unreadable: " + err.Error(),
			})
		}
		return
	}
	var state map[string]drainRecord
	if jsonErr := json.Unmarshal(raw, &state); jsonErr != nil {
		res.Dangling = append(res.Dangling, danglingRef{
			Kind: refDrainGap, ID: string(daemon.DrainGapProgressUnreadable),
			Detail: "state/drain.json does not parse: " + jsonErr.Error(),
		})
		return
	}

	for base, rec := range state {
		if rec.Offset > rec.Size {
			res.Dangling = append(res.Dangling, danglingRef{
				Kind: refDrainGap, ID: base,
				Detail: fmt.Sprintf("drain progress records offset %d past size %d, which loadState refuses",
					rec.Offset, rec.Size),
			})
			continue
		}
		fi, statErr := os.Stat(paths.Long(filepath.Join(l.Spool, base)))
		if statErr != nil {
			// A consumed-and-removed spool file whose record lingers is ordinary; only a record
			// still claiming unconsumed bytes is worth naming.
			if rec.Offset < rec.Size {
				res.Dangling = append(res.Dangling, danglingRef{
					Kind: refDrainGap, ID: base,
					Detail: fmt.Sprintf("drain progress claims %d unconsumed bytes of a spool file that is gone",
						rec.Size-rec.Offset),
				})
			}
			continue
		}
		if fi.Size() < rec.Size {
			res.Dangling = append(res.Dangling, danglingRef{
				Kind: refDrainGap, ID: base,
				Detail: fmt.Sprintf("spool file is %d bytes, below the %d-byte durable bound the drain "+
					"recorded: validateProgress refuses the spool over this", fi.Size(), rec.Size),
			})
			continue
		}
		if fi.Size() > rec.Offset {
			res.Reported = append(res.Reported, danglingRef{
				Kind: refDrainGap, ID: base, Reported: true,
				Detail: fmt.Sprintf("%s: %d spool bytes not yet replayed", daemon.DrainGapPending, fi.Size()-rec.Offset),
			})
		}
	}

	// A spool file with no record at all is not a gap: the drain has simply not reached it yet, and
	// the next pass consumes it from zero. Naming it would turn every mid-flight spool into a
	// finding.
}

// auditJournals checks that the delivery lease and ack journals still LOAD. Their contents are the
// daemon's own business — the shapes are unexported and internal/daemon owns the frontier logic —
// but a journal that will not parse is the state delivery_seal_tool.go calls "a recovery decision,
// not a repair", and GC treats an unreadable lease set as a reason to collect nothing.
//
// fsck: ABSORB, with delivery_seal_tool.go's discipline: report, take the daemon lock before any
// write, and never edit a journal — only the derived position files.
func auditJournals(res *auditResult, l paths.Layout) {
	for _, name := range []string{"delivery-leases.jsonl", "delivery-acks.jsonl"} {
		path := filepath.Join(l.State, name)
		lines, err := readJSONLines(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			res.Dangling = append(res.Dangling, danglingRef{
				Kind: refJournalUnreadable, ID: name,
				Detail: "delivery journal unreadable: " + err.Error(),
			})
			continue
		}
		for i, raw := range lines {
			var probe map[string]json.RawMessage
			if jsonErr := json.Unmarshal(raw, &probe); jsonErr != nil {
				res.Dangling = append(res.Dangling, danglingRef{
					Kind: refJournalUnreadable, ID: fmt.Sprintf("%s:%d", name, i+1),
					Detail: "delivery journal line does not parse: " + jsonErr.Error(),
				})
			}
		}
	}
}

// relativeFiles lists every regular file directly under dir, by name. A missing directory is an
// empty list, not an error: several callers ask about a directory the product may never have created.
func relativeFiles(dir string) []string {
	entries, err := os.ReadDir(paths.Long(dir))
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

// firstNonEmpty returns the first non-empty string, for identifying a reference by whichever of its
// several names survived the cut.
func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return "<unnamed>"
}

// sidecarBytesHashes returns every bytes_hash a capture sidecar on disk carries. It is what an
// evidence-class retention root resolves against: those bytes live inside the sidecar itself, under
// records/captures/, never in objects/.
func sidecarBytesHashes(root string) map[string]bool {
	out := map[string]bool{}
	dir := filepath.Join(paths.Of(root).Records, "captures")
	_ = filepath.WalkDir(paths.Long(dir), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			return nil //nolint:nilerr // a vanished entry is an observation, not a failure
		}
		raw, readErr := paths.ReadFileShared(p)
		if readErr != nil {
			return nil
		}
		var sc struct {
			BytesHash string `json:"bytes_hash"`
		}
		if json.Unmarshal(raw, &sc) == nil && sc.BytesHash != "" {
			out[sc.BytesHash] = true
		}
		return nil
	})
	return out
}

// spoolInventory counts the two spool families a drain reads, in its own order.
func spoolInventory(root string) (wal, client int) {
	for _, name := range relativeFiles(paths.Of(root).Spool) {
		switch {
		case strings.HasPrefix(name, "wal-"):
			wal++
		case strings.HasPrefix(name, "client-"):
			client++
		}
	}
	return wal, client
}

// ---------------------------------------------------------------------------
// The packaged MCP server, as a killable child
// ---------------------------------------------------------------------------

const (
	// mcpProtocolVersion is the protocol a host negotiates. It is written out rather than read from
	// the package so these records state the version the launcher was actually driven with.
	mcpProtocolVersion = "2025-06-18"
	// mcpReplyBound bounds one JSON-RPC round trip through the child.
	mcpReplyBound = 30 * time.Second
	// mcpExitBound bounds the child's exit after its stdin closes or it is killed.
	mcpExitBound = 30 * time.Second
	// mcpLineBound is the largest JSON-RPC line the child may write; a tools/list result carrying
	// eight schemas exceeds bufio's 64 KiB default.
	mcpLineBound = 8 << 20
)

// mcpChild is a running `qompack mcp` process with its stdio wired to this test. It is launched the
// way plugin/.mcp.json instructs a host to launch it — the bundled binary, the `mcp` subcommand, no
// shell — because that is the process a user's session actually speaks to.
//
// It is a pipe-backed child rather than a file-backed one, unlike run(): an MCP session is an
// interactive protocol and cannot be driven through files. WaitDelay is therefore the only bound
// available against a daemon that inherited one of these handles, and it is set for that reason.
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

	cmd := exec.Command(bin, "mcp") //nolint:gosec // G204: a binary this package assembled
	// A neutral working directory that is not the repository, so a root resolution that fell back
	// to the process cwd could not reach this checkout.
	cmd.Dir = filepath.Dir(bin)
	cmd.Env = childEnv(p.Env)
	cmd.WaitDelay = childWaitDelay

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("fault: mcp stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("fault: mcp stdout pipe: %v", err)
	}
	stderr := &lockedBuffer{}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("fault: starting %s mcp: %v", bin, err)
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
	raw, err := c.request(t, "initialize", map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "qompack-fault", "version": "0"},
	})
	if err != nil {
		t.Fatalf("fault: the mcp launcher did not answer initialize: %v; stderr:\n%s", err, c.stderr.String())
	}
	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if jsonErr := json.Unmarshal(raw, &initialized); jsonErr != nil {
		t.Fatalf("fault: decoding the initialize result: %v", jsonErr)
	}
	if initialized.ProtocolVersion != mcpProtocolVersion {
		t.Fatalf("fault: the launcher negotiated %q, not the offered %q",
			initialized.ProtocolVersion, mcpProtocolVersion)
	}
	c.send(t, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
}

// callResult is one tools/call answer as it reaches the host.
type callResult struct {
	Tool    string
	Text    string
	IsError bool
}

// call drives one tools/call and returns the envelope WITHOUT asserting anything about it. Not
// asserting is the point: the rows here exist to observe whether a refusal arrives as an
// `unavailable` body, a `miss`, or a tool error, and a helper that required success would make
// exactly those observations impossible.
func (c *mcpChild) call(t *testing.T, name string, args map[string]any) (callResult, error) {
	t.Helper()
	raw, err := c.request(t, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return callResult{Tool: name}, err
	}
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if jsonErr := json.Unmarshal(raw, &res); jsonErr != nil {
		return callResult{Tool: name}, fmt.Errorf("decoding the %s result: %w\nraw: %s", name, jsonErr, raw)
	}
	out := callResult{Tool: name, IsError: res.IsError}
	for _, block := range res.Content {
		if block.Type == "text" {
			out.Text += block.Text
		}
	}
	return out, nil
}

// errMCPGone is what request returns when the launcher's stdout closed before an answer arrived.
// It is a VALUE rather than a fatal because one row exists to kill the child mid-request and
// observe exactly this: a client that sees the session end, rather than one that hangs.
var errMCPGone = errors.New("the mcp launcher closed its stdout before answering")

// request sends one JSON-RPC request and returns its result. A protocol-level error, a closed
// stdout and an expired bound are all returned rather than fatalled.
func (c *mcpChild) request(t *testing.T, method string, params any) (json.RawMessage, error) {
	t.Helper()
	c.nextID++
	id := c.nextID
	c.send(t, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})

	timer := time.NewTimer(mcpReplyBound)
	defer timer.Stop()
	select {
	case line, ok := <-c.lines:
		if !ok {
			return nil, errMCPGone
		}
		var w struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &w); err != nil {
			return nil, fmt.Errorf("every stdout line must be JSON-RPC; got %q", line)
		}
		if len(w.Error) > 0 {
			return nil, fmt.Errorf("%s answered with a protocol error: %s", method, w.Error)
		}
		if w.ID == nil || *w.ID != id {
			return nil, fmt.Errorf("responses must arrive in request order; wanted id=%d, got %q", id, line)
		}
		return w.Result, nil
	case <-timer.C:
		return nil, fmt.Errorf("no response to %s within %s", method, mcpReplyBound)
	}
}

// send writes one JSON-RPC document to the child's stdin. A write to a killed child's pipe fails,
// and that is an observation this package makes rather than a failure: the row that kills the child
// mid-request expects it.
func (c *mcpChild) send(t *testing.T, doc map[string]any) {
	t.Helper()
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("fault: marshalling a JSON-RPC request: %v", err)
	}
	if _, err := io.WriteString(c.stdin, string(b)+"\n"); err != nil {
		t.Logf("fault: writing to the mcp launcher's stdin failed (%v); the child is gone", err)
	}
}

// kill takes away the child this package started, and ONLY that child: it holds the *exec.Cmd it
// forked itself, so there is no pid lookup here and no way for it to reach a process belonging to
// another session's suite.
func (c *mcpChild) kill(t *testing.T) {
	t.Helper()
	if c.closed || c.cmd.Process == nil {
		return
	}
	if err := c.cmd.Process.Kill(); err != nil {
		t.Logf("fault: could not kill the mcp launcher: %v", err)
	}
}

// stop closes the child down and reaps it, bounded in both halves. It runs from t.Cleanup on a path
// where something has usually already gone wrong, so it logs rather than fails: a second verdict
// there would displace the first. An expiry logged here means a child survived a Kill, which is a
// diagnostic about the process tree rather than about the case.
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
	// Drain BEFORE Wait, because Wait closes the stdout pipe once the child has exited and calling
	// it while the scanner goroutine is still reading is a use-after-close. The kill above is what
	// makes the drain terminate.
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range c.lines { //nolint:revive // draining the scanner's channel so its goroutine can exit
		}
	}()
	select {
	case <-drained:
	case <-time.After(mcpExitBound):
		t.Logf("fault: the mcp launcher's stdout was still held %s after a kill; the drain was "+
			"abandoned; stderr:\n%s", mcpExitBound, c.stderr.String())
	}

	waited := make(chan error, 1)
	go func() { waited <- c.cmd.Wait() }()
	select {
	case <-waited:
	case <-time.After(mcpExitBound):
		t.Logf("fault: the mcp launcher had not been reaped %s after a kill, so a child of this "+
			"test may have outlived it; stderr:\n%s", mcpExitBound, c.stderr.String())
	}
}

// lockedBuffer is a mutex-guarded buffer for a child's stderr. The lock is not defensive noise:
// handing exec.Cmd a Stderr that is not an *os.File makes it copy the pipe on its own goroutine,
// and every failure message here reads that buffer WHILE the child is still running.
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

// retrievalEnvelope is the union of shapes the retrieval tools render, reduced to the fields these
// rows judge. internal/mcp keeps `unavailable` (the bytes were there and are not reachable now),
// `miss` (nothing matched) and `denied` (the address was refused) strictly apart, and §7.1's
// "unsupported/degraded instead of guessing" is exactly the distinction between the first two.
//
// The `unavailable` outcome is spelled `"available": false` ON THE WIRE — internal/mcp's missBody
// carries an `Available *bool`, present only when the answer is that third one — and this struct
// modelled it as a key named `unavailable` that nothing has ever written. That made the row unable
// to RECOGNISE the right answer, which went unnoticed while the product was giving the wrong one:
// the S-3 fix landed, `expand` began answering `{"found":false,"available":false,"reason":…}`, and
// the row still recorded `failed`. The field is named after the wire now, and `status` is gone with
// it: only the capability document carries a `status`, and no retrieval envelope does.
type retrievalEnvelope struct {
	Found  *bool  `json:"found"`
	Denied bool   `json:"denied"`
	Reason string `json:"reason"`
	// Available is present, and false, exactly when the answer is `unavailable`.
	Available *bool `json:"available"`
}

// describeEnvelope names the shape a retrieval answer arrived in, for a record's Detail.
func describeEnvelope(res callResult, err error) string {
	if err != nil {
		return "transport/protocol error: " + err.Error()
	}
	var env retrievalEnvelope
	if json.Unmarshal([]byte(res.Text), &env) != nil {
		return fmt.Sprintf("isError=%v, body is not the retrieval envelope: %s", res.IsError, snippet(res.Text))
	}
	return fmt.Sprintf("isError=%v found=%v denied=%v available=%v reason=%q",
		res.IsError, boolPtr(env.Found), env.Denied, boolPtr(env.Available), env.Reason)
}

func boolPtr(b *bool) string {
	if b == nil {
		return "<absent>"
	}
	return fmt.Sprintf("%v", *b)
}

// snippet bounds a body quoted into a message.
//
// The cut is on a RUNE boundary, not a byte one. Every string this bounds is log or JSON text the
// product wrote, and the product's own messages use "—" and "…"; a byte cut lands inside one of
// those three-byte runes often enough, and the replacement character it leaves in a committed
// evidence record is indistinguishable from the product having emitted mojibake.
func snippet(s string) string {
	const limit = 240
	s = strings.TrimSpace(s)
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// heartbeatAge is how far into the past ageHeartbeat moves daemon.hb's modification time. It is
// well past daemon.staleAfter (90 s) so the staleness protocol's fourth step decides, rather than
// this package's arithmetic landing near a boundary.
const heartbeatAge = 10 * time.Minute

// ageHeartbeat moves `.qompack/run/daemon.hb`'s mtime into the past, which is what makes a lock
// whose owner is dead RECLAIMABLE by the next daemon.
//
// It exists because of something this matrix measured rather than assumed. daemon.AcquireLock's
// staleness protocol reads daemon.hb's MTIME (lock.go's staleAfter = 90 s), and a KILLED daemon
// leaves a heartbeat that is seconds old — so for a minute and a half after a crash the singleton
// lock is still honoured, every session-start finds it held, and recording does not resume. That is
// the product's own design and this package does not argue with it; what this package will not do
// is spend ninety seconds of wall clock per row waiting it out.
//
// So the row advances the CLOCK on the staleness window rather than the window itself, and the
// record says so. Nothing here asserts how long anything took: §6.1 forbids the timing claim, and
// the reclaim this makes reachable is the product's own code path either way.
func ageHeartbeat(t *testing.T, root string) {
	t.Helper()
	hb := strings.TrimSuffix(daemon.LockPath(root), ".lock") + ".hb"
	if _, err := os.Stat(paths.Long(hb)); err != nil {
		return
	}
	past := time.Now().Add(-heartbeatAge)
	if err := os.Chtimes(paths.Long(hb), past, past); err != nil {
		t.Logf("fault: could not age %s: %v; a reclaim will have to wait out the staleness window", hb, err)
		return
	}
	t.Logf("fault: aged %s past the staleness window so the next daemon may reclaim the lock", hb)
}
