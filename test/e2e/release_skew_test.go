// Release skew: the previous release's daemon serving this build's hooks, and this build's daemon
// serving the previous release's hooks, on one project store.
//
// A daemon outlives the session that started it, and a plugin update does not stop it
// (docs/architecture.md §1: "A daemon started before a plugin update keeps running its own version
// until its idle exit; the next spawn runs the new one"). So every user who updates runs, for a
// while, the NEW build's hooks against the PREVIOUS release's daemon, and a session the host has not
// restarted runs the OLD hooks against a NEW daemon, both on one project store. Nothing refuses
// either pairing — there is no IPC version handshake — and the only other cross-version tests are
// store fixtures (internal/cli/crossversion_v020_test.go), which prove a newer build READS an older
// store and say nothing about two live processes of different versions talking to each other.
// These rows are the ones that do.
//
// The previous release is not a fixture. It is built here from its git tag, exactly as the release
// builds it (doBuildPreviousRelease), so the old half of every pairing is the binary users actually
// have installed, and the row goes on meaning that after every release without anyone editing it.
package e2e

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/commands"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/testutil"
)

// previousRelease is the release this build would replace: the newest v* tag merged into HEAD that
// does not point at HEAD itself, built from that tag's own tree.
type previousRelease struct {
	// Tag is the git tag, e.g. "v0.3.2", and Version is what the release stamped into its binary:
	// the tag without its "v" (release.yml: bundle --version "${GITHUB_REF_NAME#v}").
	Tag, Version string
	// Commit is the commit the tag names, peeled through an annotated tag.
	Commit string
	// Bin is the built executable.
	Bin string
	// SHA256 is the executable's digest. It is logged so a run's record can be compared with the
	// published release archive's binary for the same target.
	SHA256 string
}

// prevReleaseOnce guards the single previous-release build this package performs, for Build's
// reason: a Go build is the most expensive thing here, and its result cannot change within one
// test binary's lifetime.
var (
	prevReleaseOnce sync.Once
	prevRelease     previousRelease
	prevReleaseDir  string
	prevReleaseErr  error
)

// prevReleaseFix is what a failure to produce the previous release says to do about it. The row
// cannot be judged without the tags, and it never skips (devtool lint's stubskips): a checkout
// with no tags is one this row cannot judge, which is a red, not a pass nobody earned.
const prevReleaseFix = "this row builds the previous release from its git tag, so it needs git on PATH and " +
	"the repository's v* tags with their history: in a clone run `git fetch --tags`, and in a GitHub " +
	"Actions job give actions/checkout `fetch-depth: 0` (`fetch-tags: true` alone, at the default depth " +
	"of 1, does not bring the tag's tree)"

// buildPreviousRelease returns the previous release, building it on first use. A failure fails the
// calling test with prevReleaseFix; it never skips.
//
// The output directory is created with os.MkdirTemp rather than t.TempDir, as doBuild's is, because
// the binary has to outlive the test that triggered the build: under -count=2 one process runs every
// test twice against one fired sync.Once. removePreviousRelease, called from TestMain, takes it away.
func buildPreviousRelease(t *testing.T) previousRelease {
	t.Helper()
	prevReleaseOnce.Do(doBuildPreviousRelease)
	if prevReleaseErr != nil {
		t.Fatalf("e2e: building the previous release: %v\n%s", prevReleaseErr, prevReleaseFix)
	}
	return prevRelease
}

// doBuildPreviousRelease is buildPreviousRelease's body, factored out so sync.Once can take it as a
// bare func().
//
// The tag's tree is extracted with `git archive`, never checked out with `git worktree add`: a
// worktree is registered in the repository it was added from, so making one would mutate the
// developer's repository, and a run killed half way would leave the registration behind. An archive
// is a read of the object database and nothing else.
//
// The build is the release build for the host target: tools/devtool's goBuildRelease
// (CGO_ENABLED=0, and goBuildArgs' -trimpath -buildvcs=false) with versionLdflags' -s -w -buildid=
// and the version stamped through -X. Those are the determinism flags packaging/README.md §4 relies
// on, so under the toolchain the release used the result is byte-identical to the published binary
// for this target — the comparison install-check.yml makes from the same tag. Measured when this row
// was written: built here on windows/amd64, v0.3.2's sha256 0e9cfc75…50c1 is the published
// qompack-plugin-0.3.2-windows-amd64.zip's bin/qompack.exe to the byte. The -X path is read from the
// tag's own go.mod, so a release made under another module path is still stamped where its own binary
// reads the version.
func doBuildPreviousRelease() {
	root, err := moduleRoot()
	if err != nil {
		prevReleaseErr = err
		return
	}
	tag, commit, err := previousReleaseTag(root)
	if err != nil {
		prevReleaseErr = err
		return
	}

	prevReleaseDir, prevReleaseErr = os.MkdirTemp("", "qompack-e2e-prevrelease-")
	if prevReleaseErr != nil {
		return
	}
	src := filepath.Join(prevReleaseDir, "src")
	if err := extractTagTree(root, tag, src); err != nil {
		prevReleaseErr = fmt.Errorf("extracting %s's tree: %w", tag, err)
		return
	}
	modPath, err := goModModulePath(filepath.Join(src, "go.mod"))
	if err != nil {
		prevReleaseErr = fmt.Errorf("%s's go.mod: %w", tag, err)
		return
	}

	version := strings.TrimPrefix(tag, "v")
	out := filepath.Join(prevReleaseDir, "bin", "qompack")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	ldflags := "-s -w -buildid= -X " + modPath + "/internal/core.Version=" + version
	cmd := exec.CommandContext(context.Background(), "go", "build", "-trimpath", "-buildvcs=false",
		"-ldflags", ldflags, "-o", out, "./cmd/qompack")
	cmd.Dir = src
	// GOFLAGS is cleared because the release sets none: inherited from a run under GOFLAGS=-race
	// (nightly's race-product-child), it would make this a race build, which CGO_ENABLED=0 refuses.
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOFLAGS=")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		prevReleaseErr = fmt.Errorf("go build of %s (%s) in %s: %w\n%s", tag, commit, src, err, stderr.String())
		return
	}
	// Only the binary has to outlive the build; the source it was built from does not.
	_ = os.RemoveAll(src)

	b, err := os.ReadFile(out)
	if err != nil {
		prevReleaseErr = err
		return
	}
	sum := sha256.Sum256(b)
	prevRelease = previousRelease{
		Tag: tag, Version: version, Commit: commit, Bin: out, SHA256: hex.EncodeToString(sum[:]),
	}
}

// removePreviousRelease deletes the directory doBuildPreviousRelease created. TestMain calls it
// after the last test, mirroring harness.go's Build/removeBuild pair.
func removePreviousRelease() {
	if prevReleaseDir != "" {
		_ = os.RemoveAll(prevReleaseDir)
	}
}

// previousReleaseTag names the newest v* tag merged into HEAD that does not point at HEAD itself,
// and the commit it names. "Not HEAD itself" is what keeps the release workflow's run meaningful: a
// tag push runs this row AT the new tag, and pairing a release with itself would test nothing.
func previousReleaseTag(repo string) (tag, commit string, err error) {
	head, err := gitOutput(repo, "rev-parse", "HEAD^{commit}")
	if err != nil {
		return "", "", err
	}
	tags, err := gitOutput(repo, "tag", "--merged", "HEAD", "--list", "v*", "--sort=-v:refname")
	if err != nil {
		return "", "", err
	}
	for _, tag := range strings.Fields(tags) {
		commit, err := gitOutput(repo, "rev-parse", tag+"^{commit}")
		if err != nil {
			return "", "", err
		}
		if commit != head {
			return tag, commit, nil
		}
	}
	return "", "", fmt.Errorf("no v* tag other than one at HEAD (%s) is merged into HEAD in %s", head, repo)
}

// gitOutput runs git in repo and returns its trimmed stdout, or an error carrying its stderr.
func gitOutput(repo string, args ...string) (string, error) {
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = repo
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s (in %s): %w\n%s", strings.Join(args, " "), repo, err, stderr.String())
	}
	return strings.TrimSpace(string(out)), nil
}

// extractTagTree writes tag's tree into dst from `git archive --format=tar`. Directories and regular
// files are all a Go module's tree holds; any other entry — a link, a device — is an error, never
// something silently left out of a build that would then not be the release's.
func extractTagTree(repo, tag, dst string) error {
	cmd := exec.CommandContext(context.Background(), "git", "archive", "--format=tar", tag)
	cmd.Dir = repo
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	archive, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("git archive %s (in %s): %w\n%s", tag, repo, err, stderr.String())
	}
	tr := tar.NewReader(bytes.NewReader(archive))
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue // git archive's leading pax header, which records the commit id
		}
		rel := filepath.FromSlash(h.Name)
		if !filepath.IsLocal(rel) {
			return fmt.Errorf("archive entry %q leaves the destination", h.Name)
		}
		target := filepath.Join(dst, rel)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeArchiveFile(target, tr, h.FileInfo().Mode().Perm()); err != nil {
				return err
			}
		default:
			return fmt.Errorf("archive entry %q has type %q, which a module's tree should not hold",
				h.Name, h.Typeflag)
		}
	}
}

// writeArchiveFile writes one regular archive entry to target with the entry's permission bits,
// owner-writable so removePreviousRelease can delete it again.
func writeArchiveFile(target string, r io.Reader, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm|0o200)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, r)
	if closeErr := f.Close(); copyErr == nil {
		copyErr = closeErr
	}
	return copyErr
}

// goModModulePath reads the module path out of a go.mod's module directive.
func goModModulePath(goMod string) (string, error) {
	f, err := os.Open(goMod)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`), nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", errors.New("no module directive")
}

// skewDaemon is what identifies the daemon serving a project: the record in the lock it holds, and
// what it says of itself over admin.ping.
//
// Owner is the 32-byte nonce AcquireLock mints for every acquisition, so it is the identity two
// daemons can never share — not even two that drew the same pid, which Windows hands out again
// freely. Versions are the other half: today the previous release and this build can carry the same
// core.Version (it is bumped only in the commit a release tags), so they cannot tell the two
// daemons apart on every run. They are asserted anyway, because on the run that matters most — the
// release workflow's, at the new tag — they differ, and a daemon of the wrong build answering then
// is exactly the failure this row exists to see.
type skewDaemon struct {
	PID         int
	Owner       string
	LockVersion string
	PingPID     int
	PingVersion string
}

// skewServingDaemon reads the daemon serving root: its lock record, and its own admin.ping answer.
// The ping is sent by this build's ipc client, so against the previous release's daemon it is one
// more cross-version round trip.
func skewServingDaemon(t *testing.T, root string) skewDaemon {
	t.Helper()
	info, ok := daemon.ReadLock(root)
	require.True(t, ok, "a serving daemon holds a readable %s", daemon.LockPath(root))

	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	c := ipc.NewClientWithOptions(addr, nil, nil, nil, ipc.ClientOptions{
		State:           ipc.State{Mode: contract.ModeFull, DaemonEnabled: true},
		ConnectDeadline: e2eRoundTripDeadline,
		AckDeadline:     e2eRoundTripDeadline,
	})
	defer func() { _ = c.Close() }()
	resp, err := c.Send(context.Background(), ipc.Request{
		Op: ipc.OpAdminPing, TS: core.NowMilli(core.SystemClock()), Reply: true,
	}, e2eRoundTripDeadline)
	require.NoError(t, err, "admin.ping to the daemon serving %s", root)
	require.True(t, resp.OK, "admin.ping answers OK unconditionally; resp.Err=%q", resp.Err)
	var ping struct {
		PID     int    `json:"pid"`
		Version string `json:"version"`
	}
	require.NoError(t, json.Unmarshal(resp.Data, &ping), "admin.ping data: %s", resp.Data)

	return skewDaemon{
		PID: info.PID, Owner: info.Owner, LockVersion: info.Version, PingPID: ping.PID, PingVersion: ping.Version,
	}
}

// skewRequireServedBy asserts the daemon serving root is the one want identifies — the same lock
// acquisition, alive, answering the ping itself — and that it reports version.
func skewRequireServedBy(t *testing.T, root string, want skewDaemon, version, what string) {
	t.Helper()
	got := skewServingDaemon(t, root)
	require.Equal(t, want.Owner, got.Owner,
		"%s: a different daemon now holds the lock (was %+v, now %+v)", what, want, got)
	require.Equal(t, want.PID, got.PID, "%s: the lock's pid changed under the same owner", what)
	require.Equal(t, got.PID, got.PingPID, "%s: the process answering admin.ping is not the lock's holder", what)
	require.True(t, e2eProcessAlive(got.PID), "%s: the lock's holder (pid %d) is not running", what, got.PID)
	require.Equal(t, version, got.LockVersion, "%s: the serving daemon's lock names another version", what)
	require.Equal(t, version, got.PingVersion, "%s: the serving daemon reports another version", what)
}

// skewStopDaemon shuts down the daemon d identifies through this build's admin.shutdown and waits,
// under testutil.ShutdownDaemonUntilGone's one definition of "gone", for it to have exited.
//
// A daemon that does not honour the request is ended by the pid in its lock — the daemon this test
// itself started, never another process — so the rest of the run and its cleanup are not left
// holding a live writer, and the row then FAILS: a daemon of one release that cannot be stopped by
// the next is a compatibility finding in its own right, not a fixture problem to work around.
func skewStopDaemon(t *testing.T, root string, d skewDaemon, what string) {
	t.Helper()
	addr, err := ipc.Resolve(root)
	require.NoError(t, err)
	out := testutil.ShutdownDaemonUntilGone(root, addr, testutil.ShutdownWait{
		Tick: e2eDaemonDownTick, Bound: e2eDaemonDownBound, RoundTrip: e2eRoundTripDeadline,
	})
	if out.Gone {
		return
	}
	killed := "and it could not be ended by pid"
	if proc, findErr := os.FindProcess(d.PID); findErr == nil && proc.Kill() == nil {
		killed = fmt.Sprintf("so it was ended by pid %d", d.PID)
	}
	require.FailNowf(t, "compatibility finding",
		"%s did not stop on this build's admin.shutdown (%s), %s",
		what, out.Describe(daemon.LockPath(root), e2eDaemonDownBound), killed)
}

// skewEndOwnDaemons is the row's last resort, run from its cleanup after e2eShutdownIfReachable: any
// daemon this row identified that STILL holds root's lock — the same Owner nonce, so the pid cannot
// have been handed to another process since — and is still running is ended by its pid, and the row
// fails for it. It does nothing on a run where every daemon stopped when asked, which skewStopDaemon
// already requires; it exists for the run that failed before reaching that check, so a daemon that
// ignores admin.shutdown is not left running past the test that started it.
func skewEndOwnDaemons(t *testing.T, root string, daemons ...skewDaemon) {
	t.Helper()
	info, ok := daemon.ReadLock(root)
	if !ok {
		return
	}
	for _, d := range daemons {
		if d.Owner == "" || d.Owner != info.Owner || !e2eProcessAlive(d.PID) {
			continue
		}
		outcome := "so it was ended by pid"
		proc, err := os.FindProcess(d.PID)
		if err == nil {
			err = proc.Kill()
		}
		if err != nil {
			outcome = "and it could not be ended by pid: " + err.Error()
		}
		t.Errorf("the daemon this row started as pid %d (version %s) was still running after admin.shutdown, %s",
			d.PID, d.LockVersion, outcome)
	}
}

// skewHook runs one hook subcommand through bin and holds it to what §2.3 owes the host on either
// side of the skew: exit 0, exactly one well-formed hookio.Output on stdout and nothing after it,
// and nothing on stderr. doHook never writes to stderr, so anything there is a failure the exit
// code was obliged to hide.
func skewHook(t *testing.T, bin string, argv []string, payload []byte, env map[string]string) hookio.Output {
	t.Helper()
	stdout, stderr, code := Run(t, bin, argv, payload, env)
	require.Equal(t, 0, code, "%s %v: §2.3 permits a hook no outcome but exit 0\nstdout:\n%s\nstderr:\n%s",
		bin, argv, stdout, stderr)
	requireLoneOutput(t, stdout)
	require.Empty(t, string(stderr), "%s %v wrote to stderr", bin, argv)
	var out hookio.Output
	require.NoError(t, json.Unmarshal(stdout, &out), "%s %v: stdout:\n%s", bin, argv, stdout)
	return out
}

// skewSessionStart runs session-start for sess through bin and asserts the daemon ANSWERED it.
// session-start answers through a SessionStart hookSpecificOutput only when a daemon replied; with
// none reachable it still exits 0, with at worst the empty object (internal/cli/sessionstart.go),
// so the shape is the evidence that this client and the serving daemon completed a reply round trip.
func skewSessionStart(t *testing.T, bin, root string, sess core.SessionID, env map[string]string) {
	t.Helper()
	out := skewHook(t, bin, []string{"session-start"}, sessionStartFor(t, root, sess), env)
	require.NotNil(t, out.HookSpecificOutput,
		"%s session-start for %s: no hookSpecificOutput, so no daemon answered it", bin, sess)
	require.Equal(t, hookio.EventSessionStart, out.HookSpecificOutput.HookEventName)
}

// skewFirstSilentPreCompact is the first release whose PreCompact hook prints nothing. Every
// release before it printed the empty object, which Claude Code 2.1.293 passes to the summarizer
// as instructions.
const skewFirstSilentPreCompact = "0.3.3"

// skewPreCompactStdout is the exact stdout the PreCompact hook of the release stamped version
// writes: "{}\n" before skewFirstSilentPreCompact, nothing from it on. It is for a released binary
// only: this build's own hook prints nothing whatever core.Version says, because the version is
// bumped only in the commit a release tags.
func skewPreCompactStdout(t *testing.T, version string) string {
	t.Helper()
	if skewVersionLess(t, version, skewFirstSilentPreCompact) {
		return "{}\n"
	}
	return ""
}

// skewVersionLess compares two MAJOR.MINOR.PATCH release versions numerically.
func skewVersionLess(t *testing.T, a, b string) bool {
	t.Helper()
	parse := func(v string) [3]int {
		parts := strings.Split(v, ".")
		require.Len(t, parts, 3, "release version %q is not MAJOR.MINOR.PATCH", v)
		var n [3]int
		for i, part := range parts {
			x, err := strconv.Atoi(part)
			require.NoError(t, err, "release version %q", v)
			n[i] = x
		}
		return n
	}
	pa, pb := parse(a), parse(b)
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}

// skewCheckpoint runs the PreCompact hook for sess through bin, holds its stdout to wantStdout byte
// for byte (the expectation follows the binary under test: skewPreCompactStdout for a released
// binary, empty for this build), and asserts it sealed a NEW checkpoint. Its host-visible answer is
// the same sealed or not (C1.12), so the artifact is the only evidence of what the serving daemon
// did with it (TestV1_HookLifecycleThroughRealBinary's PreCompact block).
func skewCheckpoint(t *testing.T, bin, wantStdout, root string, sess core.SessionID, env map[string]string) {
	t.Helper()
	before := cpCheckpointArtifacts(t, root)
	argv := []string{"checkpoint"}
	stdout, stderr, code := Run(t, bin, argv, cpPreCompactPayload(t, root, sess), env)
	require.Equal(t, 0, code, "%s %v: §2.3 permits a hook no outcome but exit 0\nstdout:\n%s\nstderr:\n%s",
		bin, argv, stdout, stderr)
	require.Empty(t, string(stderr), "%s %v wrote to stderr", bin, argv)
	require.Equal(t, wantStdout, string(stdout), "%s %v: exact PreCompact stdout", bin, argv)
	after := cpCheckpointArtifacts(t, root)
	require.Greater(t, len(after), len(before),
		"%s checkpoint for %s sealed no new checkpoint (before %v, after %v)", bin, sess, before, after)
}

// skewFlush runs the SessionEnd hook for sess through bin and waits for the serving daemon to have
// ended the session: obsRunFlush's terminal-hook marker, which the daemon writes only after the
// end's DAG flush, store flush and sketch save have returned.
func skewFlush(t *testing.T, bin, root string, sess core.SessionID, env map[string]string) {
	t.Helper()
	before := obsSessionEndMarker(root)
	skewHook(t, bin, []string{"flush"}, obsFlushPayload(t, root, sess), env)
	obsAwaitSessionEnded(t, root, sess, before)
}

// skewIndexRecord is the subset of one index/tool_use.jsonl line these rows read: the compact
// on-disk record internal/store/tooluseindex.go's tuRec writes.
type skewIndexRecord struct {
	ID      string         `json:"id"`
	Session core.SessionID `json:"s"`
	Tool    string         `json:"tool"`
}

// skewIndexed is the store's capture so far, as these rows count it: every indexed tool_use id, and
// how many verbatim prompts each session has on record.
type skewIndexed struct {
	ids     map[string]bool
	prompts map[core.SessionID]int
}

// skewReadIndex decodes index/tool_use.jsonl. A mutation record (tuSupersedeRec, which carries "op"
// and no session) names no new capture and is not counted; with distinct ids, paths and content none
// is expected anyway.
func skewReadIndex(root string) skewIndexed {
	idx := skewIndexed{ids: map[string]bool{}, prompts: map[core.SessionID]int{}}
	for _, line := range obsToolUseLines(root) {
		var rec skewIndexRecord
		if json.Unmarshal([]byte(line), &rec) != nil || rec.Session == "" {
			continue
		}
		idx.ids[rec.ID] = true
		if rec.Tool == hookio.EventUserPromptSubmit {
			idx.prompts[rec.Session]++
		}
	}
	return idx
}

// skewAwaitIndexed waits, within obsProcessBound, until the index holds every id in ids and at least
// prompts[s] verbatim prompts for each session s, driving the serving daemon's drain on each poll as
// x15v5WaitIndexed does, so a delivery that degraded to the client spool does not wait on the
// watcher's own cadence. The caller waits while the daemon under test is still the one serving, so
// what this sees indexed was indexed by that daemon.
func skewAwaitIndexed(t *testing.T, root string, ids []string, prompts map[core.SessionID]int) {
	t.Helper()
	missing := func() []string {
		idx := skewReadIndex(root)
		var out []string
		for _, id := range ids {
			if !idx.ids[id] {
				out = append(out, id)
			}
		}
		for s, n := range prompts {
			if idx.prompts[s] < n {
				out = append(out, fmt.Sprintf("%d verbatim prompt(s) of %s (have %d)", n, s, idx.prompts[s]))
			}
		}
		return out
	}
	x15v5AdminDrain(root)
	require.Eventually(t, func() bool {
		if len(missing()) == 0 {
			return true
		}
		x15v5AdminDrain(root)
		return len(missing()) == 0
	}, obsProcessBound, obsProcessTick, "the store never indexed %v: %s", skewMissing(missing), obsWaitDiag{root})
}

// skewMissing renders what skewAwaitIndexed is still missing at FORMAT time, for obsWaitDiag's
// reason: require.Eventually evaluates its message arguments when it is called, before the wait.
type skewMissing func() []string

func (m skewMissing) String() string { return strings.Join(m(), "; ") }

// skewRequireQuietLogs asserts nothing went wrong silently. Every hook exits 0 by policy (§2.3), so
// a hook's exit code cannot carry a failure; these are the places one is recorded instead:
//
//   - LOUD.log, the channel logging.Logger.Loud reserves for contract violations and degradation
//     transitions (loudLines). The hot path's own degradation needs three consecutive 512-sample
//     windows over budget (runtime.hotPath.breachWindows), which a dozen events cannot fill, so
//     nothing in this row may put a line there.
//   - logs/hook-quiet-*.jsonl, where a hook records a read, admission or transport setup failure
//     it could not report any other way (internal/cli/hookclient.go's logQuiet).
//   - an error-level line in the day log.
//
// Warn lines are LOGGED for the record, not failed: a hook's dial missing its warm-daemon deadline
// and spooling is the designed degrade on a loaded host (the reason obsProcessBound exists), and
// what this row requires of such a delivery is that it was still indexed, which skewAwaitIndexed
// checks.
func skewRequireQuietLogs(t *testing.T, root, when string) {
	t.Helper()
	require.Empty(t, loudLines(t, root), "%s: LOUD.log records a contract violation or a degradation", when)

	logs := paths.Of(root).Logs
	quiet, err := filepath.Glob(filepath.Join(logs, "hook-quiet-*.jsonl"))
	require.NoError(t, err)
	for _, q := range quiet {
		b, readErr := os.ReadFile(paths.Long(q))
		require.NoError(t, readErr)
		require.Failf(t, "a hook recorded a failure quietly", "%s: %s:\n%s", when, filepath.Base(q), b)
	}

	days, err := filepath.Glob(filepath.Join(logs, "qompack-*.log"))
	require.NoError(t, err)
	for _, day := range days {
		b, readErr := os.ReadFile(paths.Long(day))
		require.NoError(t, readErr)
		for _, line := range splitLines(string(b)) {
			switch {
			case strings.Contains(line, " level=error "):
				require.Failf(t, "an error-level line in the day log", "%s: %s: %s", when, filepath.Base(day), line)
			case strings.Contains(line, " level=warn "):
				t.Logf("%s: warn line, recorded and not failed: %s", when, line)
			}
		}
	}
}

// skewRecallFinds runs `qompack recall <marker> --json` through bin against the serving daemon and
// asserts a hit carries the marker's capture: the tool_use id itself, or — for a verbatim prompt,
// whose id the daemon derives from the turn — a UserPromptSubmit hit of the right session.
func skewRecallFinds(t *testing.T, bin string, p *testutil.Project, marker string, match func(id, tool string) bool) {
	t.Helper()
	code, envl, res := x15v5Tool(t, bin, p, "recall", marker)
	require.Equal(t, commands.ExitOK, code, "recall %q: %+v", marker, envl.Error)
	require.True(t, envl.OK, "recall %q: %+v", marker, envl.Error)
	require.False(t, res.IsError, "recall %q: %+v", marker, res)
	var body struct {
		Hits []struct {
			ToolUseID string `json:"tool_use_id"`
			Tool      string `json:"tool"`
		} `json:"hits"`
		Found bool `json:"found"`
	}
	x15v5ToolBody(t, res, &body)
	require.True(t, body.Found, "recall %q found nothing", marker)
	for _, h := range body.Hits {
		if match(h.ToolUseID, h.Tool) {
			return
		}
	}
	require.Failf(t, "recall missed a capture",
		"recall %q: no hit is the capture that carried it: %+v", marker, body.Hits)
}

// skewEvent is one capture a phase records and Phase C recalls: a tool use with a fixed id, or a
// verbatim prompt, each carrying a marker word that appears nowhere else in the run.
type skewEvent struct {
	sess   core.SessionID
	marker string
	toolID string // "" for a prompt
}

// payload renders the event's hook payload, and argv the subcommand that delivers it.
func (e skewEvent) payload(t *testing.T, root string) []byte {
	t.Helper()
	if e.toolID == "" {
		return obsPromptPayload(t, root, e.sess, "the "+e.marker+" prompt: keep the auth middleware's retry")
	}
	path := "src/" + e.marker + ".go"
	return obsToolPayload(t, root, e.sess, e.toolID, path, "package skew\n\n// "+e.marker+"\nfunc Retry() {}\n")
}

func (e skewEvent) argv() []string {
	if e.toolID == "" {
		return []string{"observe", "prompt"}
	}
	return []string{"observe", "tool"}
}

// recalled reports whether a recall hit is this event's capture.
func (e skewEvent) recalled(id, tool string) bool {
	if e.toolID != "" {
		return id == e.toolID
	}
	return tool == hookio.EventUserPromptSubmit && strings.HasPrefix(id, "prompt_"+string(e.sess)+"_")
}

// TestE2E_ReleaseSkewBothWays drives one project through both pairings an update produces, then
// checks the store they shared.
//
//   - A, the previous release's daemon serving this build's hooks: the previous release starts the
//     daemon (session-start for S1, then a prompt and a Read for S1), and this build then runs a
//     whole session, S2, against it — session-start, prompt, Read, PreCompact, Stop, SessionEnd.
//   - B, this build's daemon serving the previous release's hooks: the old daemon goes away, this
//     build's session-start for S3 starts this build's daemon, and the previous release carries on
//     S1 — a session whose host still runs the old plugin — with a prompt, a Read, a PreCompact and
//     its SessionEnd.
//   - C, the shared store: this build recalls what every phase recorded, and fsck, run with no
//     daemon, finds the store intact.
//
// Every hook call is held to §2.3 and to the evidence its exit code cannot carry: the captures
// indexed, a checkpoint sealed, the session ended, the serving daemon still the one it should be,
// and nothing recorded in the places a silent failure lands (skewRequireQuietLogs).
func TestE2E_ReleaseSkewBothWays(t *testing.T) {
	newBin := Build(t)
	prev := buildPreviousRelease(t)
	t.Logf("previous release %s at %s, built as %s (sha256 %s)", prev.Tag, prev.Commit, prev.Bin, prev.SHA256)

	stdout, stderr, code := Run(t, prev.Bin, []string{"version"}, nil, nil)
	require.Equal(t, 0, code, "stderr:\n%s", stderr)
	require.Equal(t, prev.Version, strings.TrimSpace(string(stdout)),
		"the previous release's binary must report the version its tag names")

	p := testutil.NewProject(t)
	var oldDaemon, newDaemon skewDaemon
	// Whichever daemon is serving when the row ends — or fails — must be gone before t.TempDir's
	// cleanup removes the tree it writes in (e2eShutdownIfReachable's doc comment). On Windows the
	// previous release's daemon also holds its own executable open, and with it the directory
	// removePreviousRelease deletes.
	t.Cleanup(func() {
		e2eShutdownIfReachable(t, p.Root)
		skewEndOwnDaemons(t, p.Root, oldDaemon, newDaemon)
	})
	env := e2eEnv(p)

	const (
		s1 = core.SessionID("sess-e2e-skew-s1")
		s2 = core.SessionID("sess-e2e-skew-s2")
		s3 = core.SessionID("sess-e2e-skew-s3")
	)
	var (
		aOldPrompt = skewEvent{sess: s1, marker: "skewalpha"}
		aOldTool   = skewEvent{sess: s1, marker: "skewbravo", toolID: "toolu_skew_a_old_read"}
		aNewPrompt = skewEvent{sess: s2, marker: "skewcharlie"}
		aNewTool   = skewEvent{sess: s2, marker: "skewdelta", toolID: "toolu_skew_a_new_read"}
		bOldPrompt = skewEvent{sess: s1, marker: "skewecho"}
		bOldTool   = skewEvent{sess: s1, marker: "skewfoxtrot", toolID: "toolu_skew_b_old_read"}
	)
	record := func(bin string, e skewEvent) { skewHook(t, bin, e.argv(), e.payload(t, p.Root), env) }

	if !t.Run("A_PreviousDaemonServesThisBuildsHooks", func(t *testing.T) {
		skewSessionStart(t, prev.Bin, p.Root, s1, env)
		e2eWaitDaemonUp(t, p.Root)
		oldDaemon = skewServingDaemon(t, p.Root)
		// No daemon existed before the previous release's session-start, and a daemon is its
		// spawner's own executable, so this is the previous release's daemon.
		skewRequireServedBy(t, p.Root, oldDaemon, prev.Version, "after the previous release's session-start")
		record(prev.Bin, aOldPrompt)
		record(prev.Bin, aOldTool)

		skewSessionStart(t, newBin, p.Root, s2, env)
		record(newBin, aNewPrompt)
		record(newBin, aNewTool)
		skewCheckpoint(t, newBin, "", p.Root, s2, env)
		skewHook(t, newBin, []string{"observe", "stop"}, obsStopPayload(t, p.Root, s2), env)
		skewFlush(t, newBin, p.Root, s2, env)

		skewAwaitIndexed(t, p.Root, []string{aOldTool.toolID, aNewTool.toolID},
			map[core.SessionID]int{s1: 1, s2: 1})
		skewRequireServedBy(t, p.Root, oldDaemon, prev.Version, "after this build's session S2")
		skewRequireQuietLogs(t, p.Root, "phase A")
	}) {
		return
	}

	if !t.Run("B_ThisBuildsDaemonServesPreviousHooks", func(t *testing.T) {
		skewStopDaemon(t, p.Root, oldDaemon, "the previous release's daemon")

		skewSessionStart(t, newBin, p.Root, s3, env)
		e2eWaitDaemonUp(t, p.Root)
		newDaemon = skewServingDaemon(t, p.Root)
		require.NotEqual(t, oldDaemon.Owner, newDaemon.Owner,
			"this build's session-start must have started a daemon of its own, not found the old one")
		skewRequireServedBy(t, p.Root, newDaemon, core.Version, "after this build's session-start")

		record(prev.Bin, bOldPrompt)
		record(prev.Bin, bOldTool)
		skewCheckpoint(t, prev.Bin, skewPreCompactStdout(t, prev.Version), p.Root, s1, env)
		skewFlush(t, prev.Bin, p.Root, s1, env)

		skewAwaitIndexed(t, p.Root, []string{bOldTool.toolID}, map[core.SessionID]int{s1: 2})
		skewRequireServedBy(t, p.Root, newDaemon, core.Version, "after the previous release's hooks for S1")
		skewRequireQuietLogs(t, p.Root, "phase B")
	}) {
		return
	}

	t.Run("C_SharedStoreIsIntact", func(t *testing.T) {
		for _, e := range []skewEvent{aOldPrompt, aOldTool, aNewPrompt, aNewTool, bOldPrompt, bOldTool} {
			skewRecallFinds(t, newBin, p, e.marker, e.recalled)
		}

		// fsck runs with the daemon gone, so its answer is a statement about the store and not, as
		// it says of a run beside a live daemon, a snapshot of a moving target.
		skewStopDaemon(t, p.Root, newDaemon, "this build's daemon")
		rep := installFsck(t, newBin, p.Root, p.Home())
		require.False(t, rep.DaemonRunning)
		for _, c := range rep.Checks {
			require.True(t, c.OK, "fsck row %s failed after the skewed sessions: %+v", c.ID, c)
		}
		require.Equal(t, commands.ExitOK, rep.Exit, "fsck must find the store the two releases shared intact")
		skewRequireQuietLogs(t, p.Root, "phase C")
	})
}
