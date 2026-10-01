package e2e

import (
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
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pluginmanifest"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
)

// Installation rehearsal: the shared machinery behind install_test.go (SP-17 Commit 8).
//
// Three things live here and nowhere else in this package. The first is the EVIDENCE RECORD, whose
// shape is fixed by the Task 7/Task 8 interface contract (install-record-schema.md) rather than by
// either task's own code, so `devtool release-scope` can read what this file writes without either
// side having imported the other. The second is the HOST BUNDLE: one real `devtool bundle` per
// version per test binary, because a bundle is a cross-compile plus a hash of every file in the
// tree and nothing any case does mutates it. The third is the READ-ONLY GUARD over the user's real
// ~/.claude (coordinator ruling R8-2): every host invocation this package makes runs under a
// disposable CLAUDE_CONFIG_DIR, and the guard is what proves it.
//
// Nothing here starts a live model session (R8-1). The only `claude` invocations are the plugin
// and marketplace subcommands, each with a timeout and a closed stdin.

// ── evidence records ───────────────────────────────────────────────────────────────────────────

// installArtifactsEnv names the directory records are collected into. Unset — the ordinary
// `go test` case — means one temp directory per test, so a plain run leaves nothing behind.
const installArtifactsEnv = "QOMPACK_INSTALL_ARTIFACTS"

// installRecordIndexName is the manifest of the records ONE run produced. The schema fixes it as a
// JSON ARRAY of the records themselves, in name order, so a reader that has only the index still
// has every field release-scope derives a status from.
const installRecordIndexName = "INDEX.json"

// installProvider is the host this package drives. It is the `target.provider` of every record.
const installProvider = "claude-code"

// installSkipPrefix is the stubskips-permitted prefix for an environment-gated skip
// (tools/devtool/stubskips.go); the reason after it must be non-empty.
const installSkipPrefix = "platform: "

// The four outcomes the schema permits a record to carry, and the five capabilities.
const (
	installVerified = "verified"
	installFailed   = "failed"
	installSkipped  = "skipped"

	capInstall       = "install"
	capUpgrade       = "upgrade"
	capUninstall     = "uninstall"
	capRollback      = "rollback"
	capUnknownSchema = "unknown_schema"
)

// installTarget is the host a record is attributed to. Version is the real `claude --version`
// output or "unknown" — never an empty string, which would read as "we did not look".
type installTarget struct {
	Provider string `json:"provider"`
	Version  string `json:"version"`
	Platform string `json:"platform"`
	Date     string `json:"date"`
}

// installBundleID is the artifact the case drove, read from the assembled BUNDLE.json plus the
// digest of the binary itself. Two records naming one version but different digests came from two
// different builds, and nothing else in the record would say so.
type installBundleID struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	Target       string `json:"target"`
	BinarySHA256 string `json:"binary_sha256"`
}

// installRecord is one case's retained artifact, exactly as install-record-schema.md fixes it.
type installRecord struct {
	Name       string          `json:"name"`
	Capability string          `json:"capability"`
	Scope      string          `json:"scope"`
	Outcome    string          `json:"outcome"`
	Reason     string          `json:"reason"`
	Target     installTarget   `json:"target"`
	Bundle     installBundleID `json:"bundle"`
	Artifact   string          `json:"artifact"`
}

// installRecordDirName is the committed evidence directory's name, and therefore the prefix of
// every record's own `artifact` path. It is derived from the host rather than spelled, so a record
// written on another platform cannot claim this one's directory.
func installRecordDirName() string {
	return "commit8-install-" + runtime.GOOS + "-" + runtime.GOARCH
}

var (
	// installCollectOnce guards the single prune-and-create of a collecting run's directory.
	installCollectOnce sync.Once
	installCollectDir  string
	installCollectErr  error

	// installTempDirMu guards installTempDirs, which caches one temp directory per test so a test
	// writing four records does not scatter them across four directories.
	installTempDirMu sync.Mutex
	installTempDirs  = map[string]string{}

	// installWrittenMu guards installWritten, this run's records in write order.
	installWrittenMu sync.Mutex
	installWritten   []installRecord
)

// installChildWaitDelay bounds how long Wait may go on after a child has exited. CommandContext
// kills only the direct child; on this host `claude` is an npm shim (cmd.exe → node) and `go run`
// spawns the assembled binary as a grandchild, so without WaitDelay a wedged grandchild keeps
// stdout/stderr open and the bound is not a bound. Same constant and the same ErrWaitDelay
// treatment as test/fault/fault.go.
const installChildWaitDelay = 5 * time.Second

// installArtifactDir returns where records are written.
//
// A collecting run ($QOMPACK_INSTALL_ARTIFACTS set) writes every test's records into one directory
// and TestMain writes INDEX.json over them. A plain run uses t.TempDir, so the schema's "same
// layout" cannot include INDEX.json: each test has its own directory and TestMain has no single
// path to write the index into.
func installArtifactDir(t *testing.T) string {
	t.Helper()
	if os.Getenv(installArtifactsEnv) != "" {
		installCollectOnce.Do(prepareInstallCollectDir)
		if installCollectErr != nil {
			t.Fatalf("install: %v", installCollectErr)
		}
		return installCollectDir
	}
	installTempDirMu.Lock()
	defer installTempDirMu.Unlock()
	if d, ok := installTempDirs[t.Name()]; ok {
		return d
	}
	d := t.TempDir()
	name := t.Name()
	installTempDirs[name] = d
	// The directory dies with this test, so the entry must too: under `go test -count=N` the next
	// run of the same name would otherwise be handed a directory that no longer exists.
	t.Cleanup(func() {
		installTempDirMu.Lock()
		defer installTempDirMu.Unlock()
		delete(installTempDirs, name)
	})
	return d
}

// prepareInstallCollectDir creates the collecting directory and removes the JSON an earlier run
// left in it. The prune is unconditional over *.json in that directory for test/platform's own
// reason: pruning only the names this run is about to write cannot remove a record whose CASE was
// deleted or renamed, and that is exactly the stale row which reads as current.
func prepareInstallCollectDir() {
	dir := os.Getenv(installArtifactsEnv)
	if err := os.MkdirAll(paths.Long(dir), 0o700); err != nil {
		installCollectErr = fmt.Errorf("creating the artifact directory %s: %w", dir, err)
		return
	}
	entries, err := os.ReadDir(paths.Long(dir))
	if err != nil {
		installCollectErr = fmt.Errorf("reading the artifact directory %s: %w", dir, err)
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if err := os.Remove(paths.Long(filepath.Join(dir, e.Name()))); err != nil {
			installCollectErr = fmt.Errorf("pruning the stale record %s: %w", e.Name(), err)
			return
		}
	}
	installCollectDir = dir
}

// writeInstallRecordIndex writes INDEX.json for a collecting run. TestMain calls it after the last
// case, so a case that fatalled before writing its record is visible as an absence in a document
// written afterwards rather than as nothing at all. Errors go to stderr: the records are already
// on disk, and release-scope reads the individual files, so a missing index must not be silent.
func writeInstallRecordIndex() {
	if os.Getenv(installArtifactsEnv) == "" || installCollectDir == "" {
		return
	}
	installWrittenMu.Lock()
	recs := append([]installRecord(nil), installWritten...)
	installWrittenMu.Unlock()
	sort.Slice(recs, func(i, j int) bool { return recs[i].Name < recs[j].Name })

	b, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "install: marshalling %s: %v\n", installRecordIndexName, err)
		return
	}
	if err := os.WriteFile(paths.Long(filepath.Join(installCollectDir, installRecordIndexName)), append(b, '\n'), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "install: writing %s: %v\n", installRecordIndexName, err)
	}
}

// newInstallRecord returns a record pre-filled with the host target and the bundle identity, so no
// case has to remember to attribute its own evidence. bundle may be the zero value when the case
// failed before a bundle existed.
func newInstallRecord(name, capability string, b installBundleID) installRecord {
	return installRecord{
		Name: name, Capability: capability, Scope: "installed_cli",
		Target:   installTarget{Provider: installProvider, Version: claudeCLIVersion(), Platform: runtime.GOOS + "/" + runtime.GOARCH, Date: installEvidenceDate()},
		Bundle:   b,
		Artifact: installRecordDirName() + "/" + name + ".json",
	}
}

// installEvidenceDate is the record's `target.date`, in ISO-8601, from the real clock. A record is
// a statement about when a host behaved as it did, so it takes wall-clock time and not a test seam.
func installEvidenceDate() string { return time.Now().UTC().Format(time.DateOnly) }

// writeInstallRecord persists rec and returns its path. The name is the file name, so it must be a
// single path segment: a record whose name carried a separator would write outside the directory.
func writeInstallRecord(t *testing.T, rec installRecord) string {
	t.Helper()
	switch {
	case rec.Outcome == "":
		t.Fatalf("install %s: a record must carry an outcome", rec.Name)
	case rec.Outcome != installVerified && rec.Reason == "":
		t.Fatalf("install %s: outcome %q requires a reason", rec.Name, rec.Outcome)
	case rec.Name == "" || rec.Name != filepath.Base(rec.Name) || strings.ContainsAny(rec.Name, `/\`):
		t.Fatalf("install: record name %q must be a single path segment", rec.Name)
	case rec.Name+".json" == installRecordIndexName:
		t.Fatalf("install: %q collides with this run's own manifest", rec.Name)
	}

	b, err := json.MarshalIndent(rec, "", "  ")
	require.NoError(t, err, "marshalling the %s record", rec.Name)
	p := filepath.Join(installArtifactDir(t), rec.Name+".json")
	require.NoError(t, os.WriteFile(paths.Long(p), append(b, '\n'), 0o600), "writing %s", p)

	installWrittenMu.Lock()
	installWritten = append(installWritten, rec)
	installWrittenMu.Unlock()
	t.Logf("install %s: %s — %s\n  artifact: %s", rec.Name, rec.Outcome, rec.Reason, p)
	return p
}

// recordInstallVerified is the one-line form for a case that ran and held.
func recordInstallVerified(t *testing.T, name, capability string, b installBundleID, reason string) {
	t.Helper()
	rec := newInstallRecord(name, capability, b)
	rec.Outcome, rec.Reason = installVerified, reason
	writeInstallRecord(t, rec)
}

// skipInstallHostCases writes one skipped record per install/upgrade/uninstall case with the
// schema's platform-skip reason, then skips with that exact string. One record for the whole
// test would leave the other five cases invisible to release-scope.
func skipInstallHostCases(t *testing.T, b installBundleID) {
	t.Helper()
	for _, c := range []struct{ name, cap string }{
		{"install_validate_bundle_strict", capInstall},
		{"install_marketplace_user_scope", capInstall},
		{"install_launcher_resolves_in_cache", capInstall},
		{"upgrade_marketplace_republish", capUpgrade},
		{"uninstall_keep_data", capUninstall},
		{"uninstall_default", capUninstall},
	} {
		rec := newInstallRecord(c.name, c.cap, b)
		rec.Outcome, rec.Reason = installSkipped, installSkipPrefix+"claude CLI not on PATH"
		writeInstallRecord(t, rec)
	}
	t.Skip(installSkipPrefix + "claude CLI not on PATH")
}

// skipUnknownSchemaCases writes the four unknown-schema records as skipped and skips the test.
// Those records are attributed to installed_cli; without the host CLI they must not be verified
// through the bundled launcher.
func skipUnknownSchemaCases(t *testing.T, b installBundleID) {
	t.Helper()
	for _, name := range []string{
		"unknown_schema_checkpoint_artifact",
		"unknown_schema_capture_sidecar",
		"unknown_schema_delivery_seal_v2",
		"unknown_schema_config_settings_version",
	} {
		rec := newInstallRecord(name, capUnknownSchema, b)
		rec.Outcome, rec.Reason = installSkipped, installSkipPrefix+"claude CLI not on PATH"
		writeInstallRecord(t, rec)
	}
	t.Skip(installSkipPrefix + "claude CLI not on PATH")
}

// recordInstallSkipped writes a skipped record without skipping the test: the store-API drills
// can still run through the bundled launcher when the CLI is absent; only the installed-cli
// rows (restored-root reads) must not claim verified.
func recordInstallSkipped(t *testing.T, name, capability string, b installBundleID, reason string) {
	t.Helper()
	rec := newInstallRecord(name, capability, b)
	rec.Outcome, rec.Reason = installSkipped, reason
	writeInstallRecord(t, rec)
}

// requireHostOK records a host refusal as a failed row and stops the test. A non-zero exit
// without a record would leave release-scope with an absent row rather than unverified.
func requireHostOK(t *testing.T, name, capability string, b installBundleID, res hostResult) {
	t.Helper()
	if res.Code == 0 {
		return
	}
	reason := res.Combined()
	if reason == "" {
		reason = fmt.Sprintf("claude exited %d with empty output", res.Code)
	}
	rec := newInstallRecord(name, capability, b)
	rec.Outcome, rec.Reason = installFailed, reason
	writeInstallRecord(t, rec)
	t.FailNow()
}

// failInstallRows records every caller row as failed with the host's combined output, then stops.
// marketplace-add / plugin-install refusals happen before those rows exist, and an absent row
// leaves release-scope looking at a hole instead of unverified.
func failInstallRows(t *testing.T, capability string, b installBundleID, res hostResult, names ...string) {
	t.Helper()
	reason := res.Combined()
	if reason == "" {
		reason = fmt.Sprintf("claude exited %d with empty output", res.Code)
	}
	for _, name := range names {
		rec := newInstallRecord(name, capability, b)
		rec.Outcome, rec.Reason = installFailed, reason
		writeInstallRecord(t, rec)
	}
	t.FailNow()
}

// ── the host CLI ───────────────────────────────────────────────────────────────────────────────

// installHostBound bounds one `claude plugin …` invocation. The CLI resolves a local marketplace,
// copies a bundle tree and rewrites two JSON documents; on a machine shared with other suites that
// is seconds rather than milliseconds, and a bound is what keeps a wedged child from consuming the
// package's whole timeout.
const installHostBound = 120 * time.Second

var (
	installCLIOnce sync.Once
	installCLIPath string
	installCLIVers = "unknown"
)

// findClaudeCLI resolves the host CLI and its version once per test binary. `claude --version` is
// the only invocation this package makes without a subcommand, and it is read-only: it starts no
// session, reads no project and changes no configuration (R8-1).
func findClaudeCLI() (string, string) {
	installCLIOnce.Do(func() {
		bin, err := exec.LookPath("claude")
		if err != nil {
			return
		}
		installCLIPath = bin
		probeHome, tmpErr := os.MkdirTemp("", "qompack-claude-version-")
		if tmpErr != nil {
			return
		}
		defer func() { _ = os.RemoveAll(probeHome) }()
		ctx, cancel := context.WithTimeout(context.Background(), installHostBound)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, "--version") //nolint:gosec // G204: fixed argument, path from LookPath
		cmd.Stdin = bytes.NewReader(nil)
		cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+probeHome)
		cmd.WaitDelay = installChildWaitDelay
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = io.Discard
		if runErr := cmd.Run(); runErr != nil && !errors.Is(runErr, exec.ErrWaitDelay) {
			return
		}
		// The CLI answers "2.1.263 (Claude Code)"; the leading token is the version.
		raw := strings.TrimSpace(out.String())
		installCLIVers = raw
		if f := strings.Fields(raw); len(f) > 0 {
			installCLIVers = f[0]
		}
	})
	return installCLIPath, installCLIVers
}

// claudeCLIVersion is findClaudeCLI reduced to what a record carries.
func claudeCLIVersion() string {
	_, v := findClaudeCLI()
	return v
}

// hostResult is one `claude plugin …` invocation's outcome.
type hostResult struct {
	Stdout string
	Stderr string
	Code   int
}

// Combined is the refusal text a skipped or failed record carries verbatim.
func (r hostResult) Combined() string {
	return strings.TrimSpace(strings.TrimSpace(r.Stdout) + "\n" + strings.TrimSpace(r.Stderr))
}

// runClaudePlugin drives one host subcommand under a disposable CLAUDE_CONFIG_DIR.
//
// stdin is closed, not inherited: every one of these subcommands prompts when it thinks a terminal
// is attached, and a prompt with no reader is a hang rather than a failure. The environment is the
// process's with HOME/USERPROFILE restored to the REAL ones — testutil.NewProject points them at a
// temp tree, and a CLI that resolved its own installation directory from there would reinstall
// itself mid-test — and CLAUDE_CONFIG_DIR overriding where the whole home config lives, which is
// what keeps R8-2 true by construction rather than by assertion.
func runClaudePlugin(t *testing.T, home string, args ...string) hostResult {
	t.Helper()
	bin, _ := findClaudeCLI()
	require.NotEmpty(t, bin, "runClaudePlugin needs the host CLI; callers check first")

	ctx, cancel := context.WithTimeout(context.Background(), installHostBound)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // G204: fixed subcommands, path from LookPath
	cmd.Dir = home
	cmd.Stdin = bytes.NewReader(nil)
	cmd.WaitDelay = installChildWaitDelay
	cmd.Env = append(os.Environ(),
		"CLAUDE_CONFIG_DIR="+home,
		"HOME="+realUserHome,
		"USERPROFILE="+realUserHome,
	)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &outBuf, &errBuf

	err := cmd.Run()
	res := hostResult{Stdout: outBuf.String(), Stderr: errBuf.String()}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		res.Code = 0
	case ctx.Err() != nil:
		// CommandContext kills the child when the deadline fires, so Run returns the dead
		// process's *exec.ExitError — never ctx.Err() — and an errors.As arm first would
		// swallow every timeout as an ordinary non-zero exit (test/fault/fault.go:339-345).
		res.Code = -1
		if errors.As(err, &exitErr) {
			res.Code = exitErr.ExitCode()
		}
		res.Stderr += fmt.Sprintf("\n<timed out after %s>", installHostBound)
	case errors.Is(err, exec.ErrWaitDelay):
		res.Code = 0
		t.Logf("install: claude %v exited 0 but its I/O was still open after %s (WaitDelay fired)",
			args, installChildWaitDelay)
	case errors.As(err, &exitErr):
		res.Code = exitErr.ExitCode()
	default:
		t.Fatalf("install: could not run claude %v: %v\nstderr:\n%s", args, err, res.Stderr)
	}
	t.Logf("install: claude %s -> exit %d\n%s", strings.Join(args, " "), res.Code, res.Combined())
	return res
}

// ── the read-only guard over the user's real configuration (R8-2) ──────────────────────────────

// realUserHome is the REAL user home, captured at package initialisation — before any test calls
// testutil.NewProject, which t.Setenv's HOME and USERPROFILE at a temp tree. Reading it later would
// fingerprint a directory that never existed and the guard would pass vacuously.
var realUserHome = detectRealUserHome()

func detectRealUserHome() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return ""
}

// realClaudeConfigFingerprint hashes the user's real ~/.claude/settings.json and lists
// ~/.claude/plugins/ with every entry's path and size.
//
// It is READ-ONLY and it is the whole of R8-2's mechanism: this package asserts the fingerprint is
// unchanged across every host invocation, so a subcommand that fell through to the live
// configuration — a missed CLAUDE_CONFIG_DIR, a flag the CLI ignored — fails the test rather than
// editing the user's installation. A missing file or directory fingerprints as "absent" rather
// than as an error: not having one is a legitimate state, and it has to stay the state it was.
func realClaudeConfigFingerprint(t *testing.T) string {
	t.Helper()
	if realUserHome == "" {
		return "no-user-home"
	}
	dot := filepath.Join(realUserHome, ".claude")

	var b strings.Builder
	settings := filepath.Join(dot, "settings.json")
	if raw, err := os.ReadFile(paths.Long(settings)); err == nil {
		sum := sha256.Sum256(raw)
		fmt.Fprintf(&b, "settings.json %s\n", hex.EncodeToString(sum[:]))
	} else if errors.Is(err, fs.ErrNotExist) {
		b.WriteString("settings.json absent\n")
	} else {
		t.Fatalf("install: reading %s for the R8-2 guard: %v", settings, err)
	}

	var lines []string
	pluginsDir := filepath.Join(dot, "plugins")
	err := filepath.WalkDir(paths.Long(pluginsDir), func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(paths.Long(pluginsDir), p)
		if relErr != nil {
			return relErr
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			// A file that vanished under the walk is itself a change; name it rather than failing.
			lines = append(lines, filepath.ToSlash(rel)+" vanished")
			return nil //nolint:nilerr // the absence IS the observation this guard records.
		}
		if d.IsDir() {
			lines = append(lines, filepath.ToSlash(rel)+"/")
			return nil
		}
		lines = append(lines, fmt.Sprintf("%s %d", filepath.ToSlash(rel), info.Size()))
		return nil
	})
	switch {
	case errors.Is(err, fs.ErrNotExist):
		b.WriteString("plugins/ absent\n")
	case err != nil:
		t.Fatalf("install: listing %s for the R8-2 guard: %v", pluginsDir, err)
	default:
		sort.Strings(lines)
		b.WriteString(strings.Join(lines, "\n"))
	}
	return b.String()
}

// ── the host bundle ────────────────────────────────────────────────────────────────────────────

// installBundleBound is how long one `devtool bundle` may take: a real cross-compile of
// ./cmd/qompack plus a sha256 of every file in the assembled tree.
const installBundleBound = 15 * time.Minute

// The two versions this package publishes. They are explicit rather than left to the version rule
// (`git describe`, else internal/core.Version) for one reason: the upgrade case needs a SEMVER BUMP
// of the version it just installed, and a described version carries a commit-count suffix that has
// no successor. They are stamped by the test and name no release.
const (
	installBaseVersion = "0.1.0"
	installNextVersion = "0.1.1"
)

// installedBundle is one assembled bundle: the directory a host treats as ${CLAUDE_PLUGIN_ROOT},
// the launcher inside it, and the identity every record attributes its evidence to.
type installedBundle struct {
	Dir     string
	DirBase string
	Bin     string
	ID      installBundleID
}

var (
	installBundleOnce sync.Once
	installBundleBase string
	installBundles    map[string]installedBundle
	installBundleErr  error
)

// hostBundles assembles BOTH versions of the host target's bundle once per test binary, into one
// output directory which doubles as the local marketplace root.
//
// One directory, because `claude plugin validate` refuses a marketplace whose plugin `source`
// climbs out of the marketplace root with "..": the source must be a path BENEATH the directory
// holding .claude-plugin/, so the marketplace root has to be the bundles' own parent.
func hostBundles(t *testing.T) map[string]installedBundle {
	t.Helper()
	installBundleOnce.Do(doAssembleBundles)
	if installBundleErr != nil {
		t.Fatalf("install: assembling the host bundles: %v", installBundleErr)
	}
	return installBundles
}

// hostBundle is hostBundles reduced to the base version, which is what most cases drive.
func hostBundle(t *testing.T) installedBundle {
	t.Helper()
	return hostBundles(t)[installBaseVersion]
}

// doAssembleBundles is hostBundles' body: two `go run ./tools/devtool bundle --target <host>` runs.
//
// The host target is named explicitly rather than left to the default, because the default
// assembles all six and five of them would be cross-compiles no case here can execute. `--archive`
// is NOT passed: this base carries no such flag, and the bundle DIRECTORY already holds BUNDLE.json
// and checksums.txt, which is the whole of the identity these records cite.
func doAssembleBundles() {
	repo, err := moduleRoot()
	if err != nil {
		installBundleErr = err
		return
	}
	installBundleBase, installBundleErr = os.MkdirTemp("", "qompack-install-bundle-")
	if installBundleErr != nil {
		return
	}
	installBundles = map[string]installedBundle{}
	for _, v := range []string{installBaseVersion, installNextVersion} {
		b, aerr := assembleOneBundle(repo, installBundleBase, v)
		if aerr != nil {
			installBundleErr = aerr
			return
		}
		installBundles[v] = b
	}
}

// assembleOneBundle runs the assembler for one version and reads back the identity it wrote.
func assembleOneBundle(repo, out, version string) (installedBundle, error) {
	ctx, cancel := context.WithTimeout(context.Background(), installBundleBound)
	defer cancel()

	target := runtime.GOOS + "/" + runtime.GOARCH
	cmd := exec.CommandContext(ctx, "go", "run", "./tools/devtool", "bundle", //nolint:gosec // G204: fixed argv over this package's own temp directory
		"--target", target, "--out", out, "--version", version)
	cmd.Dir = repo
	cmd.Stdin = bytes.NewReader(nil)
	// The assembly inherits the real environment deliberately: a `go run` under a temp-directory
	// HOME resolves a different module and build cache and can spend minutes re-downloading what is
	// already on disk (test/canary's doBuild gives the same reason).
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = installChildWaitDelay
	if err := cmd.Run(); err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		return installedBundle{}, fmt.Errorf(
			"go run ./tools/devtool bundle --target %s --out %s --version %s (in %s): %w\nstdout:\n%s\nstderr:\n%s",
			target, out, version, repo, err, stdout.String(), stderr.String())
	}

	base := fmt.Sprintf("qompack-plugin-%s-%s-%s", version, runtime.GOOS, runtime.GOARCH)
	dir := filepath.Join(out, base)
	var id struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Target  struct {
			OS   string `json:"os"`
			Arch string `json:"arch"`
		} `json:"target"`
	}
	raw, err := os.ReadFile(paths.Long(filepath.Join(dir, "BUNDLE.json")))
	if err != nil {
		return installedBundle{}, fmt.Errorf("reading the assembled BUNDLE.json: %w", err)
	}
	if err := json.Unmarshal(raw, &id); err != nil {
		return installedBundle{}, fmt.Errorf("parsing the assembled BUNDLE.json: %w", err)
	}
	if id.Version != version || id.Target.OS != runtime.GOOS || id.Target.Arch != runtime.GOARCH {
		return installedBundle{}, fmt.Errorf("BUNDLE.json names %s %s/%s, not %s %s",
			id.Version, id.Target.OS, id.Target.Arch, version, target)
	}
	bin := filepath.Join(dir, "bin", "qompack"+installExeSuffix())
	sum, err := installFileSHA256(bin)
	if err != nil {
		return installedBundle{}, fmt.Errorf("hashing the bundled launcher: %w", err)
	}
	return installedBundle{
		Dir: dir, DirBase: base, Bin: bin,
		ID: installBundleID{Name: id.Name, Version: id.Version, Target: target, BinarySHA256: sum},
	}, nil
}

// removeInstallBundles deletes what doAssembleBundles created. TestMain calls it after the last
// test, the way removeBuild takes away Build's output.
func removeInstallBundles() {
	if installBundleBase != "" {
		_ = os.RemoveAll(installBundleBase)
	}
}

// installExeSuffix is the host's executable extension, matching devtool's own bundleBinPath.
func installExeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// ── the disposable marketplace ─────────────────────────────────────────────────────────────────

// installMarketplaceName is the local marketplace every case publishes through. It is part of the
// host's own identity for an installed plugin (`qompack@qompack-rehearsal`) and of the cache path,
// so it is a constant rather than a per-test string.
const installMarketplaceName = "qompack-rehearsal"

// writeMarketplace generates the minimal `.claude-plugin/marketplace.json` that publishes one
// bundle directory, and returns the marketplace root.
//
// `description` is present because `--strict` treats a missing marketplace description as an ERROR,
// not a warning: without it `claude plugin validate <marketplace> --strict` reports success false
// with an otherwise clean report. The plugin `source` is a "./"-relative path beneath the
// marketplace root for the reason the CLI states in its own refusal — sources resolve against the
// root, never against marketplace.json's directory, and a ".." in one is rejected outright.
func writeMarketplace(t *testing.T, root, pluginDirBase string) string {
	t.Helper()
	doc := map[string]any{
		"name":        installMarketplaceName,
		"description": "Disposable local marketplace for the SP-17 installation rehearsal",
		"owner":       map[string]any{"name": "Qompack"},
		"plugins": []any{map[string]any{
			"name":        "qompack",
			"source":      "./" + pluginDirBase,
			"description": pluginmanifest.Description,
		}},
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	require.NoError(t, err)
	dir := filepath.Join(root, ".claude-plugin")
	require.NoError(t, os.MkdirAll(paths.Long(dir), 0o755))
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(dir, "marketplace.json")), append(b, '\n'), 0o644))
	t.Logf("install: marketplace %s publishes ./%s\n%s", installMarketplaceName, pluginDirBase, b)
	return root
}

// installCacheDir is where the host copies a bundle on install:
// <CLAUDE_CONFIG_DIR>/plugins/cache/<marketplace>/<plugin>/<version>/.
func installCacheDir(home, version string) string {
	return filepath.Join(home, "plugins", "cache", installMarketplaceName, "qompack", version)
}

// installedLauncher is the path form the manifest names — ${CLAUDE_PLUGIN_ROOT}/bin/qompack — once
// the plugin root is the host's cache directory.
func installedLauncher(home, version string) string {
	return filepath.Join(installCacheDir(home, version), "bin", "qompack"+installExeSuffix())
}

// ── file and tree digests ──────────────────────────────────────────────────────────────────────

// installFileSHA256 streams p through sha256 so an 8 MB launcher is never held in memory whole.
func installFileSHA256(p string) (string, error) {
	f, err := os.Open(paths.Long(p))
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashTree walks root and returns every regular file's digest by slash path, skipping any path
// whose first segment is in skip.
//
// It is snapshotProjectFiles' answer with CONTENT rather than presence, which is what an uninstall
// has to be judged against: a file that was rewritten in place is byte-different at a path that was
// there before, and a listing of names could never see it. paths.Long is applied at the root so a
// >260-character project is walkable on Windows.
func hashTree(t *testing.T, root string, skip ...string) map[string]string {
	t.Helper()
	skipped := make(map[string]bool, len(skip))
	for _, s := range skip {
		skipped[s] = true
	}
	out := map[string]string{}
	longRoot := paths.Long(root)
	err := filepath.WalkDir(longRoot, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(longRoot, p)
		if relErr != nil {
			return relErr
		}
		slash := filepath.ToSlash(rel)
		if slash == "." {
			return nil
		}
		if skipped[strings.SplitN(slash, "/", 2)[0]] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		sum, sumErr := installFileSHA256(p)
		if sumErr != nil {
			return sumErr
		}
		out[slash] = sum
		return nil
	})
	require.NoError(t, err, "hashing %s", root)
	return out
}

// verifyChecksumsFile checks every line of a bundle's checksums.txt against the tree it sits in,
// in `sha256sum` format — digest, two spaces, slash path — and returns how many lines it verified.
func verifyChecksumsFile(t *testing.T, dir string) int {
	t.Helper()
	raw, err := os.ReadFile(paths.Long(filepath.Join(dir, "checksums.txt")))
	require.NoError(t, err, "reading checksums.txt under %s", dir)

	n := 0
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		digest, rel, ok := strings.Cut(line, "  ")
		require.True(t, ok, "checksums.txt line %q is not `<digest>  <path>`", line)
		got, sumErr := installFileSHA256(filepath.Join(dir, filepath.FromSlash(rel)))
		require.NoError(t, sumErr, "hashing %s under %s", rel, dir)
		require.Equal(t, digest, got, "%s does not match its checksums.txt entry", rel)
		n++
	}
	require.NotZero(t, n, "checksums.txt under %s lists nothing", dir)
	return n
}

// ── reading what the host and the bundle wrote ─────────────────────────────────────────────────

// readJSONFile decodes a JSON document the host wrote, naming the file when it cannot.
func readJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := os.ReadFile(paths.Long(path))
	require.NoError(t, err, "reading %s", path)
	require.NoError(t, json.Unmarshal(raw, v), "parsing %s:\n%s", path, raw)
}

// bundlePluginVersion is the `version` of a bundle's own .claude-plugin/plugin.json — the document
// the HOST reads, as against BUNDLE.json, which is the document this repository writes. The two
// naming one version is what `devtool bundle` promises and what an install has to preserve.
func bundlePluginVersion(t *testing.T, bundleDir string) string {
	t.Helper()
	var manifest struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	readJSONFile(t, filepath.Join(bundleDir, ".claude-plugin", "plugin.json"), &manifest)
	require.Equal(t, "qompack", manifest.Name)
	return manifest.Version
}

// installPluginListEntry is one row of `claude plugin list --json`.
type installPluginListEntry struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	Scope       string `json:"scope"`
	Enabled     bool   `json:"enabled"`
	InstallPath string `json:"installPath"`
}

// installListPlugins asks the host what it believes is installed in this disposable home.
func installListPlugins(t *testing.T, home, name, capability string, b installBundleID) []installPluginListEntry {
	t.Helper()
	res := runClaudePlugin(t, home, "plugin", "list", "--json")
	requireHostOK(t, name, capability, b, res)
	var out []installPluginListEntry
	require.NoError(t, json.Unmarshal([]byte(res.Stdout), &out),
		"plugin list --json must be JSON: %s", res.Stdout)
	return out
}

// ── driving the launcher ───────────────────────────────────────────────────────────────────────

// installEnvFor is the environment a real-binary invocation against a project root runs with.
// The root need not be a testutil.Project's own — a restored backup root is a real project
// root with no Project object behind it.
func installEnvFor(root, home string) map[string]string {
	return map[string]string{"QOMPACK_PROJECT_ROOT": root, "HOME": home, "USERPROFILE": home}
}

// installedOrBundledLauncher returns the launcher the rehearsal drives and a word naming where it
// came from, so a record can say which.
//
// It prefers a REALLY INSTALLED binary — one the host CLI copied into its own cache — because the
// acceptance row is about the installed location, not about a path this repository chose. When the
// CLI is absent the bundled launcher stands in and the record says so: the file is byte-identical
// either way (the install case proves that), so what the fallback costs is the claim about the
// INSTALLATION, never the claim about the binary.
func installedOrBundledLauncher(t *testing.T, b installedBundle, capability string, names ...string) (bin, where string) {
	t.Helper()
	fingerprint := realClaudeConfigFingerprint(t)
	if cli, _ := findClaudeCLI(); cli == "" {
		return b.Bin, "bundled (claude CLI not on PATH)"
	}
	home := filepath.Join(t.TempDir(), "claude-home")
	require.NoError(t, os.MkdirAll(paths.Long(home), 0o700))
	writeMarketplace(t, filepath.Dir(b.Dir), b.DirBase)
	if add := runClaudePlugin(t, home, "plugin", "marketplace", "add", filepath.Dir(b.Dir)); add.Code != 0 {
		failInstallRows(t, capability, b.ID, add, names...)
	}
	if ins := runClaudePlugin(t, home, "plugin", "install",
		"qompack@"+installMarketplaceName, "-s", "user", "-y"); ins.Code != 0 {
		failInstallRows(t, capability, b.ID, ins, names...)
	}
	t.Cleanup(func() {
		_ = runClaudePlugin(t, home, "plugin", "uninstall", "qompack", "-s", "user", "-y")
		require.Equal(t, fingerprint, realClaudeConfigFingerprint(t),
			"R8-2: the user's real ~/.claude must be byte-identical across every host invocation")
	})
	launcher := installedLauncher(home, b.ID.Version)
	require.FileExists(t, paths.Long(launcher))
	return launcher, "installed"
}

// installSuiteSeq numbers the hook suites one test binary drives, so no two share a tool_use_id.
var installSuiteSeq atomic.Int64

// installSessionTurns is how many PostToolUse events one rehearsal session replays. It is small on
// purpose: every turn is a real process spawn, and what these tests need is a project with real
// objects, index lines, a checkpoint and spool state — not a volume measurement.
const installSessionTurns = 8

// installIndexBound and installIndexTick bound the wait for the asynchronous half of ingest.
const (
	installIndexBound = 60 * time.Second
	installIndexTick  = 100 * time.Millisecond
)

// installRunHook drives one hook subcommand through the launcher and asserts §2.3's two invariants:
// exit 0, and a stdout that is one valid hookio.Output.
func installRunHook(t *testing.T, bin string, argv []string, payload []byte, env map[string]string) {
	t.Helper()
	stdout, stderr, code := Run(t, bin, argv, payload, env)
	require.Equal(t, 0, code, "%v must exit 0\nstdout:\n%s\nstderr:\n%s", argv, stdout, stderr)
	requireParsesAsOutput(t, stdout)
}

// installSessionStartPayload is the SessionStart payload a host writes at startup.
func installSessionStartPayload(t *testing.T, root string, sess core.SessionID) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"hook_event_name": "SessionStart", "session_id": sess, "cwd": root, "source": "startup",
	})
	require.NoError(t, err)
	return b
}

// installDriveSession runs one whole session through bin against p: SessionStart brings a real
// detached daemon up, tool and prompt observations flow over the real transport, PreCompact asks
// for a checkpoint and SessionEnd flushes. Every payload carries marker so a later recall has
// something real to find.
//
// It waits for the index rather than for a clock: a hot-path call whose ACK wait expired is durable
// in the client spool and invisible in the index until the daemon's own drain, so a test that read
// the file immediately would be asserting on a race.
func installDriveSession(t *testing.T, bin, root, home string, sess core.SessionID, marker string) {
	t.Helper()
	before := len(obsToolUseLines(root))

	installRunHook(t, bin, []string{"session-start"}, installSessionStartPayload(t, root, sess),
		installEnvFor(root, home))
	e2eWaitDaemonUp(t, root)
	installHookSuite(t, bin, root, home, sess, marker, true)

	want := before + installSessionTurns
	require.Eventually(t, func() bool { return len(obsToolUseLines(root)) >= want },
		installIndexBound, installIndexTick,
		"index/tool_use.jsonl never reached %d records after a session through %s", want, bin)
}

// installHookSuite drives every hook of one session EXCEPT session-start, asserting only §2.3's two
// invariants. It is split out of installDriveSession so a caller can omit PreCompact (the rollback
// project's no-checkpoint arm) without copying the rest of the suite.
func installHookSuite(t *testing.T, bin, root, home string, sess core.SessionID, marker string, precompact bool) {
	t.Helper()
	env := installEnvFor(root, home)
	// Every suite gets its own ordinal, and it reaches the tool_use_id. Ingest's seen-set collapses
	// a repeated tool_use_id back to one dispatch — correctly, since a host that replays a delivery
	// must not double-record it — so a second suite over one project with the same ids would index
	// nothing at all and the wait in installDriveSession would expire against a healthy daemon.
	run := installSuiteSeq.Add(1)
	installRunHook(t, bin, []string{"observe", "prompt"},
		obsPromptPayload(t, root, sess, fmt.Sprintf("rehearse the installation %d: %s", run, marker)), env)
	for i := range installSessionTurns {
		installRunHook(t, bin, []string{"observe", "tool"},
			obsToolPayload(t, root, sess, fmt.Sprintf("toolu_sp17_%s_%d_%02d", sess, run, i),
				fmt.Sprintf("src/sp17_%d_%02d.ts", run, i),
				fmt.Sprintf("// %s\nexport const turn%d = %d;\n", marker, i, i)), env)
	}
	installRunHook(t, bin, []string{"observe", "stop"}, obsStopPayload(t, root, sess), env)
	if precompact {
		installRunHook(t, bin, []string{"checkpoint"}, cpPreCompactPayload(t, root, sess), env)
	}
	installRunHook(t, bin, []string{"flush"}, obsFlushPayload(t, root, sess), env)
}

// installDriveSessionWithoutCheckpoint is installDriveSession with the PreCompact hook left out, so
// the project records real objects, index lines and spool state but seals no checkpoint artifact.
//
// The sealed-checkpoint project now restores (RestoreBackup writes protected paths through
// paths.CreateNew). This variant still exists because the daemon-alive refusal, the after-first-
// new-write drill and identity parity through the installed launcher are distinct properties and
// are cheaper to demonstrate on a project that did not also seal.
func installDriveSessionWithoutCheckpoint(t *testing.T, bin, root, home string, sess core.SessionID, marker string) {
	t.Helper()
	before := len(obsToolUseLines(root))
	installRunHook(t, bin, []string{"session-start"}, installSessionStartPayload(t, root, sess),
		installEnvFor(root, home))
	e2eWaitDaemonUp(t, root)
	installHookSuite(t, bin, root, home, sess, marker, false)

	want := before + installSessionTurns
	require.Eventually(t, func() bool { return len(obsToolUseLines(root)) >= want },
		installIndexBound, installIndexTick,
		"index/tool_use.jsonl never reached %d records after a session through %s", want, bin)
}

// installRootHashes is every content root index/roots.jsonl names, as a set of hash strings. It is
// the live project's side of the identity-parity comparison a restored root has to satisfy.
func installRootHashes(t *testing.T, root string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).Index, "roots.jsonl")))
	require.NoError(t, err, "reading index/roots.jsonl under %s", root)

	out := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec struct {
			Root string `json:"root"`
			Hash string `json:"hash"`
		}
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		for _, h := range []string{rec.Root, rec.Hash} {
			if h != "" {
				out[h] = true
			}
		}
	}
	return out
}

// rollbackDrillLines returns every line migrate/rollback.jsonl holds. The log is append-only, so a
// later rehearsal can never overwrite an earlier one's finding — which is what makes counting them
// a meaningful assertion about a REFUSAL having been recorded rather than swallowed.
func rollbackDrillLines(t *testing.T, root string) []string {
	t.Helper()
	raw, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).Migrate, "rollback.jsonl")))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	require.NoError(t, err)
	var out []string
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

// ── fsck and recall through the launcher ───────────────────────────────────────────────────────

// installFsckCheck and installFsckReport mirror the fields of `qompack fsck --json` this package
// reads. They are declared here rather than imported because internal/cli's own types are
// unexported and because a test that shared them could not notice a field being renamed.
type installFsckCheck struct {
	ID string `json:"id"`
	OK bool   `json:"ok"`
	// Severity is a NUMBER on the wire: internal/contract.Severity is a uint8 with no String
	// method by design (00-ARCHITECTURE.md §5.19 leaves the user-facing rendering to SP-14's
	// /qompack:status), so a reader that declared it a string fails to decode the document.
	Severity int      `json:"severity"`
	Count    int      `json:"count"`
	Detail   []string `json:"detail"`
}

type installFsckReport struct {
	Schema        int                `json:"schema"`
	Project       string             `json:"project"`
	DaemonRunning bool               `json:"daemon_running"`
	ReadOnly      bool               `json:"read_only"`
	Checks        []installFsckCheck `json:"checks"`
	Exit          int                `json:"exit"`
}

// installFsck runs the read-only integrity scan over root through bin and returns the decoded
// report. The process exit code and the report's own `exit` field must agree — a report that said
// one thing while the process said another would make every citation of it ambiguous.
func installFsck(t *testing.T, bin, root, home string) installFsckReport {
	t.Helper()
	stdout, stderr, code := Run(t, bin, []string{"fsck", "--project", root, "--json"}, nil,
		installEnvFor(root, home))
	var rep installFsckReport
	require.NoError(t, json.Unmarshal(stdout, &rep), "fsck --json must be JSON:\n%s\nstderr:\n%s", stdout, stderr)
	require.Equal(t, rep.Exit, code, "the process exit code must equal the report's own")
	require.True(t, rep.ReadOnly, "a default fsck run writes nothing")
	return rep
}

// installFsckRow returns the named row, failing when the report does not carry it: a check that
// silently did not run is a check nobody may cite.
func installFsckRow(t *testing.T, rep installFsckReport, id string) installFsckCheck {
	t.Helper()
	for _, c := range rep.Checks {
		if c.ID == id {
			return c
		}
	}
	require.FailNowf(t, "missing fsck row", "the report carries no %q row: %+v", id, rep.Checks)
	return installFsckCheck{}
}

// installAssertStatusJSON decodes `qompack status --json` and asserts the envelope names the
// status command, reports ok, and carries one hook row per manifest entry point.
func installAssertStatusJSON(t *testing.T, stdout []byte) {
	t.Helper()
	var doc struct {
		Schema  int    `json:"schema"`
		Command string `json:"command"`
		OK      bool   `json:"ok"`
		Data    struct {
			Hooks []json.RawMessage `json:"hooks"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(stdout, &doc), "status --json must decode: %s", stdout)
	require.Equal(t, "status", doc.Command, "status --json must name the status command")
	require.True(t, doc.OK, "status --json must be ok")
	require.Equal(t, len(pluginmanifest.HookEntryPoints()), len(doc.Data.Hooks),
		"status --json must carry one hook row per manifest entry point")
}

// installAssertDoctorJSON decodes `qompack doctor --json` and asserts the project equals root and
// that a checkpoint.latest row exists. json.Valid alone is decorative.
func installAssertDoctorJSON(t *testing.T, stdout []byte, root string) {
	t.Helper()
	var doc struct {
		Project  string `json:"project"`
		Sections []struct {
			Rows []struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"rows"`
		} `json:"sections"`
	}
	require.NoError(t, json.Unmarshal(stdout, &doc), "doctor --json must decode: %s", stdout)
	require.Equal(t, root, doc.Project, "doctor --json project must be the root we asked about")
	var latest string
	for _, sec := range doc.Sections {
		for _, row := range sec.Rows {
			if row.ID == "checkpoint.latest" {
				latest = row.Status
			}
		}
	}
	require.NotEmpty(t, latest, "doctor must carry a checkpoint.latest row")
}

// installViolation is one row of state/config-violations.json. The product writes config.Violation
// with no JSON tags, so the fields are Key and Message with capital letters.
type installViolation struct {
	Key     string `json:"Key"`
	Message string `json:"Message"`
}

// installConfigViolations reads state/config-violations.json.
func installConfigViolations(t *testing.T, root string) []installViolation {
	t.Helper()
	raw, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).State, "config-violations.json")))
	require.NoError(t, err, "state/config-violations.json must exist after a versioned-section reset")
	var rows []installViolation
	require.NoError(t, json.Unmarshal(raw, &rows), "state/config-violations.json must parse:\n%s", raw)
	return rows
}

// installFsckFailures renders every failing row, for the message of an assertion that expected none.
func installFsckFailures(rep installFsckReport) string {
	var b strings.Builder
	for _, c := range rep.Checks {
		if c.OK {
			continue
		}
		fmt.Fprintf(&b, "\n  %s (severity %d, count %d): %s", c.ID, c.Severity, c.Count, strings.Join(c.Detail, "; "))
	}
	if b.Len() == 0 {
		return "(no failing row)"
	}
	return b.String()
}

// installRecallHashes brings a daemon up over root and asks the launcher's own MCP server to recall
// marker, returning the root hash of every hit.
//
// It goes through `qompack mcp` rather than through the store directly because that is the surface
// a host actually retrieves through: the stdio process transcodes, the daemon holds the warm store
// handle, and a hit is a POINTER — which is exactly what makes the hash comparable across a restore.
func installRecallHashes(t *testing.T, bin, root, home, marker string) []string {
	t.Helper()
	env := installEnvFor(root, home)
	installRunHook(t, bin, []string{"session-start"},
		installSessionStartPayload(t, root, installSession), env)
	e2eWaitDaemonUp(t, root)

	child := mcpE2EStartWithEnv(t, bin, env)
	child.send(t, mcpE2ERequest(t, 1, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"clientInfo":      map[string]string{"name": "qompack-sp17-install", "version": "1.0"},
	}))
	child.await(t, 1)
	// A notification carries NO id and must produce no response; sending it through
	// mcpE2ERequest would make it a request the server rightly answers "unknown method".
	child.send(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)

	var recalled struct {
		Hits []struct {
			Hash string `json:"hash"`
			Path string `json:"path"`
		} `json:"hits"`
		Count int  `json:"count"`
		Found bool `json:"found"`
	}
	mcpE2ECall(t, child, 2, mcp.ToolRecall, map[string]any{"query": marker, "k": 10}, &recalled)
	child.finish(t)

	out := make([]string, 0, len(recalled.Hits))
	for _, h := range recalled.Hits {
		out = append(out, h.Hash)
	}
	return out
}

// ── the declared legacy snapshot ───────────────────────────────────────────────────────────────

// installLegacyRecords is how many records the rehearsal's declared legacy snapshot carries, and
// installBackupID names the backup every drill in this package restores.
const (
	installLegacyRecords = 3
	installBackupID      = "sp17-pre-cutover"
)

// installLegacySource is a declared legacy snapshot held in memory: an ordered run of records, a
// snapshot identity and a stable frontier.
//
// Every record declares UNKNOWN fidelity, which is the case an import must never upgrade — plan §4's
// "without converting missing/unknown fidelity into exactness", asserted here by carrying it into a
// real import rather than by describing it.
type installLegacySource struct {
	id   string
	recs []store.LegacyRecord
}

func newInstallLegacySource(n int) *installLegacySource {
	src := &installLegacySource{id: "legacy-snapshot-sp17-install"}
	for i := 1; i <= n; i++ {
		src.recs = append(src.recs, store.LegacyRecord{
			ID:       fmt.Sprintf("legacy-%03d", i),
			Position: int64(i),
			Tool:     "FileRead",
			Path:     fmt.Sprintf("src/legacy%d.ts", i),
			Session:  core.SessionID("sess-legacy-sp17"),
			Turn:     core.TurnIndex(i),
			TS:       core.UnixMilli(1_700_000_000_000 + int64(i)),
			Fidelity: core.FidelityUnknown,
			Payload:  []byte(fmt.Sprintf("legacy record %d\n%s\n", i, installMarker)),
		})
	}
	return src
}

func (f *installLegacySource) Snapshot(context.Context) (store.LegacySnapshot, error) {
	return store.LegacySnapshot{ID: f.id, Frontier: int64(len(f.recs)), Records: int64(len(f.recs))}, nil
}

func (f *installLegacySource) Read(_ context.Context, after int64, limit int) ([]store.LegacyRecord, error) {
	var out []store.LegacyRecord
	for _, r := range f.recs {
		if r.Position <= after {
			continue
		}
		if len(out) == limit {
			break
		}
		out = append(out, r)
	}
	return out, nil
}

// newInstallProject creates a real project on disk with source files of its own and returns its
// root and the home directory the launcher resolves the user-global layer from.
//
// It is deliberately NOT testutil.NewProject. That constructor opens a logger over
// <root>/.qompack/logs/ and holds the day log open until its t.Cleanup runs, which on Windows makes
// the "delete my data by hand" step of the uninstall rehearsal fail with a sharing violation on a
// handle the PRODUCT does not hold — the test harness's own. A project the launcher is the only
// writer of has no such handle, and the deletion is then the operator's deletion rather than an
// approximation of it.
func newInstallProject(t *testing.T) (root, home string) {
	t.Helper()
	base := t.TempDir()
	root, home = filepath.Join(base, "project"), filepath.Join(base, "home")
	require.NoError(t, os.MkdirAll(paths.Long(root), 0o700))
	require.NoError(t, os.MkdirAll(paths.Long(home), 0o700))
	for rel, body := range installProjectFiles {
		p := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(p)), 0o700))
		require.NoError(t, os.WriteFile(paths.Long(p), []byte(body), 0o600))
	}
	return root, home
}

// installMigrator opens a Migrator over an already-recorded project with the legacy-import BUILD
// gate explicitly opened.
//
// Coordinator ruling R8-4 asked what happens if the gate cannot be opened from an external test
// package. It can: config.LegacyImportGate() is exported and MigrationGate.Passed is an exported
// field, so this opens the gate exactly the way internal/store's own drill does — by constructing a
// passed gate and saying out loud that a gated capability is being run. There is no test-only bypass
// door, and nothing here is forked from the package under test.
func installMigrator(t *testing.T, p *testutil.Project) (store.Store, *store.Migrator) {
	t.Helper()
	s, m, closeFn := installOpenMigrator(t, p)
	t.Cleanup(closeFn)
	return s, m
}

// installOpenMigrator opens a store and a migrator the caller must close. Use it when a daemon
// will run on the same root: p.Store(t) holds append handles until the test ends, which races
// the daemon (single-writer). Open after the daemon-alive refusal, or close before starting one.
func installOpenMigrator(t *testing.T, p *testutil.Project) (store.Store, *store.Migrator, func()) {
	t.Helper()
	gate := config.LegacyImportGate()
	gate.Passed = true
	s, err := store.Open(p.Root, p.Cfg, store.Deps{Log: p.Log, Clock: p.Clock})
	require.NoError(t, err, "opening a store over %s", p.Root)
	m, err := store.NewMigrator(s, p.Root, store.MigrateOptions{
		Source: newInstallLegacySource(installLegacyRecords), Gate: gate, Clock: p.Clock, Batch: 2,
	})
	if err != nil {
		_ = s.Close()
	}
	require.NoError(t, err)
	return s, m, func() { _ = s.Close() }
}

// installImportAndBackup runs the bounded import and takes the verified backup every drill in this
// package restores.
func installImportAndBackup(t *testing.T, m *store.Migrator, id string) {
	t.Helper()
	ctx := context.Background()
	imported, err := m.Import(ctx)
	require.NoError(t, err)
	require.Equal(t, installLegacyRecords, imported.Imported)
	_, err = m.TakeBackup(ctx, id)
	require.NoError(t, err)
	man, err := m.VerifyBackup(id)
	require.NoError(t, err)
	require.NotEmpty(t, man.Files, "a backup of a recorded project is not empty")
}

// installStopWriters is the drill's "stop every writer that would be incompatible after a rollback"
// step, implemented as the real check rather than as a stub: it refuses while a qompack daemon still
// holds the project's lock, and succeeds once nothing does.
func installStopWriters(root string) func(context.Context) error {
	return func(context.Context) error {
		if pid, held := testutil.DaemonHoldingLock(root); held {
			return fmt.Errorf("the qompack daemon (pid %d) still holds %s", pid, daemon.LockPath(root))
		}
		return nil
	}
}
