// V5 §4.16 — the product write set with wave 4 resident: every write the plugin makes, through
// the composed daemon and through the shipped one, lands in §13 invariant 7's enumerated set,
// each one under a directory paths.Layout names, with the project itself untouched.
//
// Successor to TestV4_LiveSessionWriteSetAppendOnlyAndImmutability (v4_x11) and
// TestV3_LiveSessionWriteSetAndAppendOnly (v3_x09), whose x9Snapshot / x9ReadLogs / x9ListFiles /
// x9AssertBinaryHasNoNetworkImports helpers this row reuses rather than re-deriving. What is new:
//
//   - a third snapshot root: the built binary's own directory, which harness.go's Run makes every
//     child's working directory. A cwd-relative write — the shape a write into the plugin install
//     directory would take — lands there and nowhere else, so it is now visible;
//   - the write set is graded against paths.Layout, not against the ".qompack/" prefix alone. A
//     path created under .qompack/ must be the self-ignoring .gitignore or live under a directory
//     the Layout names, so "explicit allowed data writes" is the product's own enumeration and the
//     wave-4 files (state/observations.json, state/promotions.json, records/… sidecars,
//     sketches/seg-NNNN.bloom) earn their place through it rather than through a prefix;
//   - the SHIPPED composition — `qompack daemon` spawned by the binary, with the scheduler, the
//     contract monitor and the MCP wiring the in-process rig does not transcribe — is driven as a
//     second arm, because the rig covers only the wave-3 part of internal/cli's runDaemon;
//   - the negative control is a REAL product write outside the set. `qompack eval import --to` is
//     the one command whose destination is user-directed and outside .qompack/ by design, and the
//     confinement check must name what it wrote. The same command's refusal to write inside the
//     qompack repository is asserted alongside, on a fixture tree that carries the module line.
//
// HISTORICAL NAMES RETIRED. `sketches/segments/` is `sketches/seg-NNNN.bloom` (store.SegmentFilterRef)
// and sits behind the default-off segmentBloom switch; `state/warmstart.json` never existed — the
// scheduler persists state/bocd.json and state/scheduler.json, and the warm prior sits behind the
// default-off warmPrior switch. Neither switch is flipped here: a refused feature is recorded as
// off, never asserted as passed. The disposition file carries the full old-to-new map.
package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/testutil"
)

// The session identities: one per arm, so a state file's name says which arm wrote it.
const (
	x16v5Session     = core.SessionID("sess-e2e-v5-x16")
	x16v5ShipSession = core.SessionID("sess-e2e-v5-x16-shipped")
	x16v5FaultSess   = core.SessionID("sess-e2e-v5-x16-fault")
)

// x16v5Turns is the hook burst each arm replays: enough that the append-only logs are genuinely
// appended to between the two log readings, and small enough that every one is a real process
// spawn the row can afford (v4_x11 sizes its burst the same way).
const x16v5Turns = 8

// x16v5FixtureModule is the fixture project's go.mod. It carries the qompack module line ON
// PURPOSE: eval.Import refuses a destination inside any tree whose go.mod says this, and the
// refusal arm below needs a tree that says it without being the real checkout.
const x16v5FixtureModule = "module github.com/qompack/qompack\n\ngo 1.26\n"

// x16v5Fixture is the project every arm runs over. Every file is re-read at the end and must be
// byte-identical: "no project mutation" is asserted on these directly, not inferred.
var x16v5Fixture = map[string]string{
	".gitignore":  "/node_modules\n/dist\n",
	"README.md":   "# fixture\n\nThe plugin must never write here.\n",
	"go.mod":      x16v5FixtureModule,
	"src/main.go": "package main\n\nfunc main() {}\n",
}

// x16v5Transcript is one recorded Claude Code session in the shape
// testdata/fixtures/transcripts/basic.jsonl has: enough turns that eval.Import writes one real
// session file to its destination, with no secret-shaped content anywhere in it.
const x16v5Transcript = `{"type":"user","message":{"role":"user","content":"please fix the auth bug"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Reading the handler."},{"type":"tool_use","id":"toolu_01","name":"Read","input":{"file_path":"src/auth/handler.go"}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01","content":"package auth\n\nfunc Handle() {}"}]}}
{"type":"user","message":{"role":"user","content":"thanks"}}
`

// x16v5Reporter is the slice of *testing.T the confinement check reports through. It is an
// interface for exactly one reason: the negative control runs the SAME check against a recorder
// and proves it reports a real stray write, which a check bound to *testing.T could only do by
// failing the test that asked.
type x16v5Reporter interface {
	Helper()
	Errorf(format string, args ...any)
}

// x16v5Recorder collects Errorf calls instead of failing anything.
type x16v5Recorder struct{ msgs []string }

func (*x16v5Recorder) Helper() {}

func (r *x16v5Recorder) Errorf(format string, args ...any) {
	r.msgs = append(r.msgs, fmt.Sprintf(format, args...))
}

// x16v5Roots names the snapshot roots by index, in x9Snapshot's key order: 0 is the project, 1 is
// HOME, 2 is the binary's directory (every child's cwd), and anything after that is an extra root
// the caller wants watched — where NOTHING may ever be written.
type x16v5Roots struct {
	Project, Home, BinDir string
	Extra                 []string
}

func (r x16v5Roots) list() []string {
	return append([]string{r.Project, r.Home, r.BinDir}, r.Extra...)
}

// name turns a snapshot key back into the absolute path it fingerprinted.
func (r x16v5Roots) name(key string) string {
	idx, rel, _ := strings.Cut(key, "|")
	all := r.list()
	for i, root := range all {
		if idx == fmt.Sprint(i) {
			return filepath.Join(root, filepath.FromSlash(rel))
		}
	}
	return key
}

// x16v5Snapshot fingerprints every root, in index order.
func x16v5Snapshot(t *testing.T, roots x16v5Roots) map[string]uint64 {
	t.Helper()
	return x9Snapshot(t, roots.list()...)
}

// x16v5Confined is §13 invariant 7's write set as a predicate over a snapshot key: locations 1
// and 2 (the project's .qompack/ and HOME's .qompack/) and nothing else. Locations 3–5 are POSIX
// socket files under $XDG_RUNTIME_DIR or the temp dir, which no snapshot root here covers — so a
// socket cannot be mistaken for a stray write, and a stray write cannot hide as a socket.
func x16v5Confined(key string) bool {
	return strings.HasPrefix(key, "0|.qompack/") || strings.HasPrefix(key, "1|.qompack/")
}

// x16v5AssertConfined asserts invariant 7 over two snapshots: every path created, modified or
// deleted between them is confined. It reports every offender rather than the first, so a failure
// reads as the whole stray write set.
func x16v5AssertConfined(rep x16v5Reporter, before, after map[string]uint64, roots x16v5Roots) {
	rep.Helper()
	for key, sum := range after {
		was, existed := before[key]
		switch {
		case !existed && !x16v5Confined(key):
			rep.Errorf("write-set: %s was CREATED outside .qompack/", roots.name(key))
		case existed && was != sum && !x16v5Confined(key):
			rep.Errorf("write-set: %s was MODIFIED outside .qompack/", roots.name(key))
		}
	}
	for key := range before {
		if _, still := after[key]; !still && !x16v5Confined(key) {
			rep.Errorf("write-set: %s was DELETED outside .qompack/", roots.name(key))
		}
	}
}

// x16v5LayoutDirs is every directory paths.Layout names, as a basename relative to .qompack/.
// It is built from paths.Of itself so the allowlist cannot drift from the product's own layout.
func x16v5LayoutDirs(l paths.Layout) []string {
	dirs := []string{
		l.Objects, l.Index, l.Sketches, l.DAG, l.Grammar, l.Checkpoints, l.Pins, l.Eval,
		l.Records, l.State, l.Run, l.Spool, l.Logs, l.Metrics, l.Tmp, l.Migrate, l.Backup,
	}
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		out = append(out, filepath.Base(d))
	}
	return out
}

// x16v5AllowedDataWrite reports whether rel — slash form, relative to <project>/.qompack — is an
// explicitly allowed data write: the self-ignoring .gitignore, or a file under a Layout directory.
// A file dropped at the top of .qompack/ by a writer that invented its own location is exactly
// what this refuses.
func x16v5AllowedDataWrite(l paths.Layout, rel string) bool {
	if rel == ".gitignore" {
		return true
	}
	for _, dir := range x16v5LayoutDirs(l) {
		if strings.HasPrefix(rel, dir+"/") {
			return true
		}
	}
	return false
}

// x16v5HomeAllowed is the complete list of files the product may write under HOME's .qompack/:
// the user-global calibration document (tokens.DefaultCalibPath). The user-global config layer is
// read there, never written. Anything else is a writer that invented a location.
var x16v5HomeAllowed = map[string]bool{"calibration.json": true}

// x16v5Delta returns the sorted names created or modified under root index idx's .qompack/
// between two snapshots, relative to that .qompack/.
func x16v5Delta(before, after map[string]uint64, idx int) []string {
	prefix := fmt.Sprintf("%d|.qompack/", idx)
	var out []string
	for key, sum := range after {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if was, existed := before[key]; existed && was == sum {
			continue
		}
		out = append(out, strings.TrimPrefix(key, prefix))
	}
	sort.Strings(out)
	return out
}

// x16v5AssertAllowedDataWrites grades one arm's whole delta against the Layout and the HOME
// allowlist, logs it (the disposition file quotes these lists), and returns the project delta so
// the caller can require the writers it drove to have actually shown up.
func x16v5AssertAllowedDataWrites(t *testing.T, before, after map[string]uint64, root string) []string {
	t.Helper()
	l := paths.Of(root)
	delta := x16v5Delta(before, after, 0)
	t.Logf("project write set (%d files): %v", len(delta), delta)
	for _, rel := range delta {
		require.True(t, x16v5AllowedDataWrite(l, rel),
			"%s was written under .qompack/ but under no directory paths.Layout names: an "+
				"explicit allowed data write lives in a Layout directory, never at an invented path", rel)
	}
	homeDelta := x16v5Delta(before, after, 1)
	t.Logf("HOME write set (%d files): %v", len(homeDelta), homeDelta)
	for _, rel := range homeDelta {
		require.True(t, x16v5HomeAllowed[rel],
			"%s was written under ~/.qompack/, which holds only the calibration document", rel)
	}
	return delta
}

// x16v5RequireWritten asserts that a writer the arm drove really produced a file: without this
// the allowlist would also pass for an arm that wrote nothing.
func x16v5RequireWritten(t *testing.T, delta []string, prefix, why string) {
	t.Helper()
	for _, rel := range delta {
		if strings.HasPrefix(rel, prefix) {
			return
		}
	}
	require.Failf(t, "an expected data write is missing",
		"nothing under %q was written: %s; the arm wrote %v", prefix, why, delta)
}

// x16v5AssertProjectUntouched re-reads every fixture file and the self-ignoring .gitignore.
func x16v5AssertProjectUntouched(t *testing.T, root string) {
	t.Helper()
	for rel, want := range x16v5Fixture {
		got, err := os.ReadFile(paths.Long(filepath.Join(root, filepath.FromSlash(rel))))
		require.NoError(t, err, "fixture %s must still exist", rel)
		require.Equal(t, want, string(got), "fixture %s must be byte-identical: no project mutation", rel)
	}
	gitignore, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(root).Dot, ".gitignore")))
	require.NoError(t, err)
	require.Equal(t, "*\n", string(gitignore), ".qompack/.gitignore must still self-ignore (§3.3)")
	require.Empty(t, x9ListFiles(t, paths.Of(root).Tmp),
		"tmp/ holds staging files only while a write is in flight; once every write has landed it is empty")
}

// x16v5AssertGrowthOnly asserts the append-only logs read mid-session are a byte prefix of the
// same logs at the end, exactly as v4_x11 asserts it.
func x16v5AssertGrowthOnly(t *testing.T, mid, end map[string][]byte) {
	t.Helper()
	checked := 0
	for rel, midBytes := range mid {
		endBytes, ok := end[rel]
		require.True(t, ok, "%s existed mid-session and must not have been removed", rel)
		require.True(t, strings.HasPrefix(string(endBytes), string(midBytes)),
			"%s must still begin with exactly the bytes it held mid-session: a differing prefix means "+
				"a line was rewritten in place", rel)
		checked++
	}
	require.Positive(t, checked, "at least one append-only log must have existed mid-session")
}

// x16v5Project is the fixture project with the e2e shutdown cleanup every daemon-driving arm needs.
func x16v5Project(t *testing.T) *testutil.Project {
	t.Helper()
	p := testutil.NewProject(t, testutil.WithFiles(x16v5Fixture))
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.Root) })
	return p
}

// x16v5Command runs one non-hook subcommand through the real binary and requires exit 0.
func x16v5Command(t *testing.T, bin string, env map[string]string, argv ...string) string {
	t.Helper()
	stdout, stderr, code := Run(t, bin, argv, nil, env)
	require.Equal(t, 0, code, "%v must exit 0\nstdout:\n%s\nstderr:\n%s", argv, stdout, stderr)
	return string(stdout)
}

// x16v5Event marshals a hook event the way the host writes it.
func x16v5Event(t *testing.T, name string, sess core.SessionID, root string) []byte {
	t.Helper()
	return x9Event(t, hookio.Event{HookEventName: name, SessionID: sess, CWD: root})
}

// TestV5_NoPackageWritesOutsideDotQompack is V5-VERIFY §4.16.
func TestV5_NoPackageWritesOutsideDotQompack(t *testing.T) {
	bin := Build(t)
	binDir := filepath.Dir(bin)

	// ── Arm 1: the composed daemon (checkpoint, rehydrate, pins, SP-14 commands) ─────────────────
	t.Run("ComposedDaemon", func(t *testing.T) {
		p := x16v5Project(t)
		roots := x16v5Roots{Project: p.Root, Home: p.Home(), BinDir: binDir}
		before := x16v5Snapshot(t, roots)

		r := v4StartRig(t, p)
		env := e2eEnv(p)

		obsRunHook(t, bin, []string{"session-start"}, sessionStartFor(t, p.Root, x16v5Session), env)
		obsRunHook(t, bin, []string{"observe", "prompt"},
			obsPromptPayload(t, p.Root, x16v5Session, "keep every write inside .qompack while wave 4 is resident"), env)
		r.SeedTurns(t, x16v5Session, "v5x16", x16v5Turns)

		// The SP-14 surfaces, through the binary, against the rig's daemon: status reads and pin
		// writes. The rig installs no MCP retrieval layer (v4StartRig documents why), so recall
		// must say so honestly — a non-zero exit naming the offline retrieval, never a success
		// with nothing in it, and never a write anywhere to compensate. The shipped arm below runs
		// the same command at exit 0 against the composition that does wire it.
		x16v5Command(t, bin, env, "status")
		x16v5Command(t, bin, env, "status", "--json")
		x16v5Command(t, bin, env, "pin", "the write set is exactly five locations")
		recallOut, _, recallCode := Run(t, bin, []string{"recall", "handler"}, nil, env)
		require.NotEqual(t, 0, recallCode, "retrieval is not wired in this arm, so recall must not report success")
		require.Contains(t, string(recallOut), "retrieval is temporarily offline",
			"an unavailable retrieval is reported as unavailable, not as absence: %s", recallOut)

		midLogs := x9ReadLogs(t, p.Root)

		// More traffic, then the wave-3/4 writers all take their turn: a sealed checkpoint, a
		// rehydration (SP-11's drop report and rehydrate state), and the flush.
		r.SeedTurns(t, x16v5Session, "v5x16b", x16v5Turns)
		cpCloseObserverSegment(t, r.Segs, x16v5Session)
		cpCloseSegment(t, r.Segs, x16v5Session, 0, 9) // turns 0..9 of the first burst, as v4_x11 closes them
		r.RunIdle(t)
		r.PreCompact(t, x16v5Session)
		require.NotEmpty(t, cpCheckpointArtifacts(t, p.Root),
			"the arm needs a sealed checkpoint so the checkpoint writer is in the write set")
		r.CompactStart(t, x16v5Session)
		obsRunHook(t, bin, []string{"flush"}, obsFlushPayload(t, p.Root, x16v5Session), env)

		// SETTLE BEFORE SNAPSHOT — do not drop this, and do not move it below the assertions.
		//
		// The flush hook process has exited, but the daemon it handed the event to has not finished:
		// the drain, the ingest WAL close, the sketch saves, metrics.Persist, the state.bin removal,
		// the server close and the lock release all still run after it (internal/daemon/daemon.go's
		// awaitStopCleanup says so in as many words, and t.Cleanup's shutdown runs only AFTER every
		// assertion below). Each of those stages under .qompack/tmp/: paths.WriteAtomic's
		// "wa-<random>" (internal/paths/atomic.go), the store's novel objects "obj-<12 hex>"
		// (internal/store/objects.go), the pins view and the bloom replacement. Two things below
		// cannot survive that. x16v5Snapshot walks the tree and x9Snapshot hard-fails on a walk
		// error, so a staging file that is unlinked between the readdir and the stat fails the row;
		// and x16v5AssertProjectUntouched ends in require.Empty over tmp/, which fires on the mere
		// EXISTENCE of a staging file — the whole duration of every in-flight write, not a narrow
		// lstat window.
		//
		// e2eShutdownIfReachable returns only once the daemon has actually finished, not once it has
		// stopped answering, which is what makes "tmp/ is empty" a statement about what the run
		// LEAKED rather than about what it happened to have in flight. The ShippedDaemon arm below
		// settles the same way, for the same reason, before its own snapshot. Arms 3 and 4 need no
		// settle and get none: the directed-import arm never touches IPC, and the severed-writers
		// arm runs under the daemon-down fault, whose spawn site is a no-op.
		//
		// Unlike that arm's, this daemon is IN-PROCESS (v4StartRig), so daemon.lock records the test
		// binary's own pid (internal/daemon/lock.go), and "has that pid exited?" could never be
		// answered yes from inside the test that is asking. The helper therefore counts our own pid
		// as settled once the lock is gone (e2eShutdownProcessSettled says why), and the settle lands
		// in milliseconds: admin.shutdown runs daemon.Stop, whose LAST act is Lock.Release, so the
		// lock's disappearance already proves every cleanup step above it has run.
		e2eShutdownIfReachable(t, p.Root)

		after := x16v5Snapshot(t, roots)
		x16v5AssertConfined(t, before, after, roots)
		delta := x16v5AssertAllowedDataWrites(t, before, after, p.Root)
		x16v5RequireWritten(t, delta, "index/tool_use.jsonl", "the observer indexed the burst")
		x16v5RequireWritten(t, delta, "checkpoints/", "the PreCompact sealed an artifact")
		x16v5RequireWritten(t, delta, "pins/", "`qompack pin` recorded an invariant")
		x16v5RequireWritten(t, delta, "state/", "the draft, rehydrate and contract state files")

		x16v5AssertProjectUntouched(t, p.Root)
		x16v5AssertGrowthOnly(t, midLogs, x9ReadLogs(t, p.Root))
		// p.AssertAppendOnly is NOT run here, for the reason v4_x11 omits it: its first probe is
		// "the FIRST write of checkpoints/0001.json succeeds through CreateNew", and this arm has
		// just sealed a real 0001.json. The §7.4 conformance list runs in the shipped arm, whose
		// daemon seals nothing.
	})

	// ── Arm 2: the SHIPPED daemon, spawned by the binary, with everything runDaemon wires ────────
	t.Run("ShippedDaemon", func(t *testing.T) {
		p := x16v5Project(t)
		roots := x16v5Roots{Project: p.Root, Home: p.Home(), BinDir: binDir}
		before := x16v5Snapshot(t, roots)
		env := e2eEnv(p)

		obsRunHook(t, bin, []string{"session-start"}, sessionStartFor(t, p.Root, x16v5ShipSession), env)
		e2eWaitDaemonUp(t, p.Root)
		obsRunHook(t, bin, []string{"observe", "prompt"},
			obsPromptPayload(t, p.Root, x16v5ShipSession, "the shipped composition writes only the product write set"), env)
		for i := range x16v5Turns {
			obsRunHook(t, bin, []string{"observe", "tool"},
				obsToolPayload(t, p.Root, x16v5ShipSession, fmt.Sprintf("toolu_v5x16s_%02d", i),
					fmt.Sprintf("src/ship_%02d.go", i),
					fmt.Sprintf("package ship\n\nfunc handler%02d() error { return nil }\n", i)), env)
		}
		obsRunHook(t, bin, []string{"observe", "stop"}, x16v5Event(t, "Stop", x16v5ShipSession, p.Root), env)
		require.Eventually(t, func() bool { return len(obsToolUseLines(p.Root)) >= x16v5Turns },
			obsProcessBound, obsProcessTick, "the shipped daemon never indexed the burst; LOUD: %v", loudLines(t, p.Root))

		// The SP-14 surfaces against the composition that wires retrieval: status, recall and
		// dropped all answer at exit 0 here. --k is passed explicitly: without it the frontend
		// sends k=0 and the daemon's schema refuses it ("/k: below minimum") — an SP-14 defect
		// this row records in its disposition rather than works around silently.
		x16v5Command(t, bin, env, "status")
		x16v5Command(t, bin, env, "status", "--json")
		x16v5Command(t, bin, env, "recall", "--k", "5", "handler")
		x16v5Command(t, bin, env, "dropped")
		midLogs := x9ReadLogs(t, p.Root)

		obsRunHook(t, bin, []string{"flush"}, x16v5Event(t, "SessionEnd", x16v5ShipSession, p.Root), env)
		// The daemon's own shutdown writes — scheduler state, lock release — are part of the set.
		e2eShutdownIfReachable(t, p.Root)

		after := x16v5Snapshot(t, roots)
		x16v5AssertConfined(t, before, after, roots)
		delta := x16v5AssertAllowedDataWrites(t, before, after, p.Root)
		x16v5RequireWritten(t, delta, "index/tool_use.jsonl", "the shipped observer indexed the burst")
		// The writers the rig does not transcribe, and the historical row could only name by
		// guessed paths: SP-12's scheduler persists state/bocd.json and state/scheduler.json (the
		// real successor of the never-written "state/warmstart.json"); SP-19's contract monitor
		// writes state/observations.json and run/marker.json.
		x16v5RequireWritten(t, delta, "state/scheduler.json", "the scheduler persisted its state on shutdown")
		x16v5RequireWritten(t, delta, "state/bocd.json", "the change-point detector persisted its state")
		x16v5RequireWritten(t, delta, "state/observations.json", "the contract monitor recorded its observations")
		x16v5RequireWritten(t, delta, "run/marker.json", "the contract monitor wrote its run marker")

		x16v5AssertProjectUntouched(t, p.Root)
		x16v5AssertGrowthOnly(t, midLogs, x9ReadLogs(t, p.Root))
		// The §7.4 conformance list, over the shipped daemon's own store now that it has exited:
		// O_TRUNC on a checkpoint, an in-place pins rewrite, WriteAtomic onto sketches/tried.bloom,
		// and CreateNew twice on the same checkpoint seq — each refused.
		p.AssertAppendOnly(t)
		x9AssertBinaryHasNoNetworkImports(t, bin)
	})

	// ── NEGATIVE CONTROL 1: a real, user-directed write outside the set must be SEEN ─────────────
	//
	// `qompack eval import --to <dir>` is the product's one write whose destination is chosen by
	// the user and lies outside .qompack/ by design. Run against a recorder, the confinement check
	// must name every file it wrote, or the passing arms above prove nothing about the check.
	t.Run("NegativeControl_DirectedImportIsOutsideAndSeen", func(t *testing.T) {
		p := testutil.NewProject(t, testutil.WithFiles(x16v5Fixture))
		base := filepath.Dir(p.Root)
		from := filepath.Join(base, "transcripts")
		to := filepath.Join(base, "directed-sessions")
		require.NoError(t, os.MkdirAll(paths.Long(from), 0o700))
		require.NoError(t, os.MkdirAll(paths.Long(to), 0o700))
		require.NoError(t, os.WriteFile(paths.Long(filepath.Join(from, "basic.jsonl")), []byte(x16v5Transcript), 0o600))
		roots := x16v5Roots{Project: p.Root, Home: p.Home(), BinDir: binDir, Extra: []string{to}}
		env := e2eEnv(p)

		before := x16v5Snapshot(t, roots)
		stdout, stderr, code := Run(t, bin, []string{"eval", "import", "--from", from, "--to", to}, nil, env)
		require.Equal(t, 0, code, "the directed import must succeed\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		after := x16v5Snapshot(t, roots)

		written := x9ListFiles(t, to)
		require.NotEmpty(t, written, "the import must have written a real session file to its destination")

		rec := &x16v5Recorder{}
		x16v5AssertConfined(rec, before, after, roots)
		require.Len(t, rec.msgs, len(written),
			"NEGATIVE CONTROL: the confinement check must report exactly the files the import wrote "+
				"outside .qompack/ — one CREATED line per file, and nothing else on any root: %v", rec.msgs)
		for _, m := range rec.msgs {
			require.Contains(t, m, "CREATED outside", "a directed write is a creation: %s", m)
			require.Contains(t, m, to, "the report must name the destination: %s", m)
		}

		// The refusal: the same command must not write inside a tree whose go.mod carries the
		// qompack module line — the fixture project's does — and must leave no trace of trying.
		inside := filepath.Join(p.Root, "sessions")
		stdout, stderr, code = Run(t, bin, []string{"eval", "import", "--from", from, "--to", inside}, nil, env)
		require.NotEqual(t, 0, code, "an import into the repository working tree must be refused")
		// eval.ImportCommand writes its own diagnostic to the command's writer, so it is on stdout.
		require.Contains(t, string(stdout)+string(stderr), "refusing to import",
			"the refusal must say why\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		_, statErr := os.Stat(paths.Long(inside))
		require.True(t, os.IsNotExist(statErr), "a refused import must not create its destination")
		x16v5AssertConfined(t, after, x16v5Snapshot(t, roots), roots)
		x16v5AssertProjectUntouched(t, p.Root)
	})

	// ── NEGATIVE CONTROL 2: with the transport severed AND the spool failing, still confined ─────
	//
	// daemon-down replaces the spawn with a no-op so no daemon answers; disk-full makes the very
	// first spool append fail. The hook has nowhere to put the event. §13 invariant 6 says it
	// exits 0 anyway, and invariant 7 says the failure path writes nowhere new: no index line, no
	// project file, nothing in the binary's directory.
	t.Run("NegativeControl_SeveredWritersStayConfined", func(t *testing.T) {
		p := testutil.NewProject(t, testutil.WithFiles(x16v5Fixture))
		roots := x16v5Roots{Project: p.Root, Home: p.Home(), BinDir: binDir}
		env := e2eEnv(p)
		env[qompackFaultEnvKey] = "daemon-down,disk-full"

		before := x16v5Snapshot(t, roots)
		stdout, stderr, code := Run(t, bin, []string{"observe", "tool"},
			obsToolPayload(t, p.Root, x16v5FaultSess, "toolu_v5x16_fault", "src/fault.go", "package fault\n"), env)
		require.Equal(t, 0, code, "a hook exits 0 even with nowhere to write\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		requireParsesAsOutput(t, stdout)
		after := x16v5Snapshot(t, roots)

		x16v5AssertConfined(t, before, after, roots)
		delta := x16v5AssertAllowedDataWrites(t, before, after, p.Root)
		for _, rel := range delta {
			require.False(t, strings.HasPrefix(rel, "index/"),
				"nothing reached the index: the event was never durably accepted, so it must not be claimed; wrote %v", delta)
		}
		x16v5AssertProjectUntouched(t, p.Root)
	})
}
