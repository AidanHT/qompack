package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// TestPlatform_ProjectRootShapes is SP17-M7-02's path axis: the shapes of project root a user
// actually has, driven through the ASSEMBLED bundle's binary rather than a `go build` of
// cmd/qompack, because the bundle is what ships (packaging/README.md §1).
//
// The four shapes are not arbitrary. A plain root is the control — without it a failure in any of
// the other three cannot be attributed to the shape. A root with spaces is `C:\Program Files\…`
// and `~/Library/Application Support/…`. A non-ASCII root is packaging/README.md §7's open
// question 3, and it additionally exercises whatever console code page a Windows host hands a
// child process. A root past MAX_PATH is open question 4: internal/paths already carries the
// `\\?\` handling the product side needs, and whether it survives a real spawn — cmd.Path, the
// environment, the daemon's own re-derivation of the root from QOMPACK_PROJECT_ROOT — is exactly
// what no in-process unit test can say.
//
// Each shape runs the whole of the brief's sequence: all six hooks exit 0 with parseable output,
// `.qompack/` exists beneath the root, the write set stayed confined, a daemon came up on the
// address the root resolves to, and one admin.ping round trip answered from it.
func TestPlatform_ProjectRootShapes(t *testing.T) {
	b := assembledBundle(t)

	cases := []struct {
		sub, record, rootName, why string
	}{
		{"plain", "path-root-plain", plainRootName, "an ordinary ASCII project root (the control)"},
		{"spaces", "path-root-spaces", spacedRootName, "a project root containing spaces"},
		{"unicode", "path-root-unicode", unicodeRootName, "a project root containing non-ASCII characters"},
		{"long", "path-root-long", longRootName(), "a project root whose absolute path exceeds MAX_PATH"},
	}

	for _, tc := range cases {
		t.Run(tc.sub, func(t *testing.T) {
			rec := newRecord(t, tc.record)
			p := newProject(t, tc.rootName)

			if tc.sub == "long" {
				require.Greater(t, len(p.Root), maxPathThreshold,
					"the long-root case must actually exceed MAX_PATH or it is the plain case again")
			}
			if tc.sub == "unicode" {
				require.False(t, isASCII(p.Root), "the unicode-root case must contain non-ASCII characters")
			}

			exerciseRootShape(t, b, p, rec, tc.why)
		})
	}
}

// exerciseRootShape drives one project root through the full hook lifecycle and records what
// happened.
//
// The order matters in two places. The write-set snapshot is taken AFTER the daemon has been shut
// down, not while it is live: everything the daemon writes belongs under `.qompack/` and would be
// permitted anyway, but walking a tree a live process is still writing to produces a snapshot of a
// moment that never existed, and the diff would be noise rather than evidence. And the daemon is
// brought up through a second `session-start` rather than by polling after the first: EnsureRunning
// already waits for its own spawn, so re-running the designated daemon starter is the product's own
// answer to "make sure one is running", not a test-only shortcut around it.
func exerciseRootShape(t *testing.T, b bundle, p project, rec Record, why string) {
	t.Helper()

	// Registered before anything spawns, so a fatal assertion below still leaves no daemon holding
	// this project's directory when the enclosing temp base is removed.
	t.Cleanup(func() { shutdownIfReachable(t, p.Root) })

	before := snapshotTree(p.Root, p.Home)

	// cmd.Dir is the BUNDLE directory, not the project: a Windows process cannot be started with a
	// working directory past MAX_PATH (CreateProcess's lpCurrentDirectory takes no `\\?\` prefix),
	// so using the project root here would make the long-root case fail to spawn for a reason that
	// has nothing to do with the product. QOMPACK_PROJECT_ROOT decides the root instead, which is
	// paths.Resolve's highest-precedence input, and the payload's cwd names the same directory.
	for _, h := range hookSubcommands {
		stdout, stderr, code := run(t, b.Bin, b.Dir, h.argv, hookPayload(t, h.event, p.Root), p.Env)
		require.Equal(t, 0, code,
			"hook %q must exit 0 for root %q (§13 invariant 6)\nstdout:\n%s\nstderr:\n%s",
			h.event, p.Root, stdout, stderr)
		parseHookOutput(t, h.event+" in "+p.Root, stdout)
	}

	require.DirExists(t, paths.Long(paths.Of(p.Root).Dot),
		"the product's store must exist beneath the project root after a session (§3.3)")

	// The designated daemon starter, run again so this assertion waits on the product's own
	// readiness rule rather than on a bare poll.
	stdout, stderr, code := run(t, b.Bin, b.Dir, []string{"session-start"},
		hookPayload(t, "SessionStart", p.Root), p.Env)
	require.Equal(t, 0, code, "session-start must exit 0\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	parseHookOutput(t, "session-start", stdout)

	addr, up := waitDaemonUp(t, p.Root)
	require.True(t, up, "a daemon must answer %s within %s for root %q", addr.Path, daemonUpBound, p.Root)

	resp, ok := adminPing(t, p.Root, addr)
	require.True(t, ok, "admin.ping must be answered by the daemon, not spooled (response %+v)", resp)

	shutdownIfReachable(t, p.Root)

	// The snapshot walks the project root and HOME and nothing else, so what this establishes is
	// bounded accordingly: nothing was written outside `.qompack/` WITHIN THOSE TWO TREES. It is
	// not a statement about the whole filesystem — proving that needs test/guards' in-process run,
	// which shares this process's working directory and can therefore see a stray relative-path
	// write land anywhere. Both halves are worth having and neither substitutes for the other; the
	// records say which one they carry.
	after := snapshotTree(p.Root, p.Home)
	permitted := productWriteSet(p.Root, p.Home)
	offenders := writeSetOffenders(before, after, permitted)
	require.Empty(t, offenders,
		"§13 invariant 7: within the project root and HOME trees nothing may be written outside %v — offenders: %v",
		permitted, offenders)

	// Non-vacuity. An empty offender list proves nothing if the run wrote nothing at all, which is
	// exactly how a silently-inert hook would pass this check.
	require.NotZero(t, changedUnder(before, after, permitted),
		"the run must have written SOMETHING under the product write set, or this case is vacuous")

	rec.Outcome = OutcomeVerified
	rec.Reason = why
	rec.Detail = fmt.Sprintf(
		"root=%q (%d chars); six hooks exit 0 with parseable hookio.Output; %s exists; "+
			"daemon reachable at %s; admin.ping answered (mode=%s hot=%s); within the project root "+
			"and HOME trees (the only two walked) nothing was written outside %v",
		p.Root, len(p.Root), paths.Of(p.Root).Dot, addr.Path, resp.Mode, hotName(resp.Hot), permitted)
	writeRecord(t, rec)
}

// changedUnder counts the paths under one of the permitted prefixes that were created or modified.
func changedUnder(before, after map[string]string, permitted []string) int {
	n := 0
	for p, sum := range after {
		if !underAny(p, permitted) {
			continue
		}
		if old, ok := before[p]; !ok || old != sum {
			n++
		}
	}
	return n
}

// isASCII reports whether s is entirely ASCII, used only to keep the Unicode case honest.
func isASCII(s string) bool {
	return utf8.RuneCountInString(s) == len(s)
}

// TestPlatform_MixedCaseProjectRoot is the case-folding row of SP17-M7-02, and it is the one case
// in this package where two INDEPENDENT facts have to be reconciled rather than asserted.
//
// The filesystem's case behaviour is a property of the volume: NTFS and APFS are usually
// case-insensitive but either can be created case-sensitive, and a Linux checkout can sit on an
// ext4 casefold directory or a CIFS mount. The product's project-key normalization is a property of
// GOOS: internal/ipc/resolve.go's normalizeRoot lower-cases when `goos == windows || goos == darwin`
// and never consults the volume. On the ordinary machine the two agree, and the interesting
// question is what the product then does with a second spelling. Where they DISAGREE the mismatch
// is a real product characteristic — and a product characteristic must not surface here as a red
// test, because a red test says "this build is broken" when what happened is "this volume is
// unusual". It is recorded as a `failed` record owned by internal/ipc instead, and the test stays
// green.
//
// Both facts are OBSERVED, neither is assumed: the filesystem by probing it (caseInsensitiveFS),
// the product by asking ipc.ProjectHash12 whether it folds the two spellings together. Hardcoding
// the GOOS rule here would just be a second copy of the thing under test.
//
// This case deliberately does NOT set QOMPACK_PROJECT_ROOT. The environment override is
// paths.Resolve's highest-precedence input, so with it set the payload's cwd never decides
// anything and the case would assert nothing about case folding at all: the spelling would simply
// be whatever the test typed into the variable. Without it, the `.git` marker newProjectAt creates
// stops the upward walk at the root and the cwd spelling is what reaches ipc.Resolve.
func TestPlatform_MixedCaseProjectRoot(t *testing.T) {
	b := assembledBundle(t)
	rec := newRecord(t, "path-root-mixed-case")

	base := tempBase(t)
	p := newProjectAt(t, base, mixedCaseRootName)
	lowerPath := filepath.Join(base, strings.ToLower(mixedCaseRootName))
	require.NotEqual(t, p.Root, lowerPath, "the two spellings must differ as strings")

	insensitive := caseInsensitiveFS(t, base)
	hashAsCreated := ipc.ProjectHash12(p.Root)
	hashLower := ipc.ProjectHash12(lowerPath)
	foldsCase := hashAsCreated == hashLower

	t.Cleanup(func() {
		shutdownIfReachable(t, p.Root)
		shutdownIfReachable(t, lowerPath)
	})

	// The disagreement case, recorded rather than failed. Both directions are real hazards and the
	// record names the one that actually applies, because the consequence is different each way.
	if foldsCase != insensitive {
		var hazard string
		if insensitive {
			hazard = "the volume is case-INSENSITIVE but the product does not fold case, so two " +
				"spellings of ONE directory take two project keys — two daemons and two stores over " +
				"one project tree"
		} else {
			hazard = "the volume is case-SENSITIVE but the product folds case, so two DIFFERENT " +
				"directories share one project key — two projects served by one daemon and one store"
		}
		rec.Outcome = OutcomeFailed
		rec.Reason = "the product's project-key normalization is keyed on GOOS, not on the filesystem, " +
			"and on this volume the two disagree"
		rec.Detail = fmt.Sprintf(
			"filesystem probe says case-insensitive=%t; ipc.ProjectHash12 folds the two spellings "+
				"together=%t (%q -> %s, %q -> %s). %s. OWNER: internal/ipc — normalizeRoot "+
				"(internal/ipc/resolve.go) lower-cases on goos windows||darwin and never asks the volume; "+
				"test/platform records this and does not fix it. No product behaviour was exercised at "+
				"the second spelling, because which behaviour would be correct is exactly what is in "+
				"question",
			insensitive, foldsCase, p.Root, hashAsCreated, lowerPath, hashLower, hazard)
		writeRecord(t, rec)
		t.Logf("FINDING (returned, not fixed): %s", rec.Reason)
		return
	}

	// HOME is pinned, but the project root is left to resolution so the cwd spelling decides it.
	env := map[string]string{"HOME": p.Home, "USERPROFILE": p.Home}

	// The as-created spelling, driven from a working directory spelled the same way.
	stdout, stderr, code := run(t, b.Bin, p.Root, []string{"session-start"},
		hookPayload(t, "SessionStart", p.Root), env)
	require.Equal(t, 0, code, "session-start must exit 0\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	parseHookOutput(t, "session-start ("+p.Root+")", stdout)

	addr, up := waitDaemonUp(t, p.Root)
	require.True(t, up, "a daemon must answer %s for the as-created spelling", addr.Path)

	var detail string
	if insensitive {
		// One directory, one key. The lower-case spelling must reach the SAME daemon, and no
		// second one may appear: two daemons over one project tree would be two writers on one
		// store.
		stdout, stderr, code = run(t, b.Bin, lowerPath, []string{"session-start"},
			hookPayload(t, "SessionStart", lowerPath), env)
		require.Equal(t, 0, code,
			"session-start must exit 0 for the lower-case spelling\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		parseHookOutput(t, "session-start ("+lowerPath+")", stdout)

		lowerAddr, err := ipc.Resolve(lowerPath)
		require.NoError(t, err)
		require.Equal(t, addr.Path, lowerAddr.Path,
			"both spellings must resolve to one transport address")

		pid, held := daemonHoldingLock(p.Root)
		require.True(t, held, "one live daemon must hold the lock after both spellings")

		resp, ok := adminPing(t, p.Root, addr)
		require.True(t, ok, "admin.ping must be answered after both spellings (response %+v)", resp)

		detail = fmt.Sprintf(
			"case-insensitive volume and case-folding normalization agree: %q and %q share "+
				"ipc.ProjectHash12 %s and transport %s; session-start ran at BOTH spellings and one "+
				"daemon (pid %d) served both, answering admin.ping (mode=%s hot=%s)",
			p.Root, lowerPath, hashAsCreated, addr.Path, pid, resp.Mode, hotName(resp.Hot))
	} else {
		// Two directories, two keys — and this branch is the one ubuntu-latest runs, so it must
		// exercise the product at the second spelling rather than compare two hashes in process.
		//
		// While only the first project's daemon is up, its address answers and the second's must
		// not: that is the distinctness assertion, and it needs exactly one daemon alive at a time.
		_, statErr := os.Stat(lowerPath)
		require.Error(t, statErr,
			"on a case-sensitive volume the lower-case spelling must not exist until this test creates it")

		lowerAddr, err := ipc.Resolve(lowerPath)
		require.NoError(t, err)
		require.NotEqual(t, addr.Path, lowerAddr.Path,
			"two different project directories must resolve to two different transport addresses")
		require.False(t, ipc.Probe(lowerAddr, probeTimeout),
			"the first project's daemon must NOT answer the second project's address")

		resp, ok := adminPing(t, p.Root, addr)
		require.True(t, ok, "admin.ping must be answered for the first spelling (response %+v)", resp)

		// Hand the first project's daemon back before starting the second's, so this case never
		// holds two at once.
		shutdownIfReachable(t, p.Root)

		lowerProject := newProjectAt(t, base, strings.ToLower(mixedCaseRootName))
		require.Equal(t, lowerPath, lowerProject.Root)

		stdout, stderr, code = run(t, b.Bin, lowerProject.Root, []string{"session-start"},
			hookPayload(t, "SessionStart", lowerProject.Root), env)
		require.Equal(t, 0, code,
			"session-start must exit 0 for the second project\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		parseHookOutput(t, "session-start ("+lowerProject.Root+")", stdout)

		secondAddr, secondUp := waitDaemonUp(t, lowerProject.Root)
		require.True(t, secondUp, "a daemon must answer %s for the second project", secondAddr.Path)
		secondResp, secondOK := adminPing(t, lowerProject.Root, secondAddr)
		require.True(t, secondOK, "admin.ping must be answered for the second project (response %+v)", secondResp)

		// Each project got its own store; neither wrote into the other's.
		require.DirExists(t, paths.Long(paths.Of(p.Root).Dot),
			"the first project must have its own .qompack")
		require.DirExists(t, paths.Long(paths.Of(lowerProject.Root).Dot),
			"the second project must have its own .qompack")

		shutdownIfReachable(t, lowerProject.Root)

		detail = fmt.Sprintf(
			"case-sensitive volume and non-folding normalization agree: %q and %q are two projects "+
				"with two ipc.ProjectHash12 values (%s vs %s) and two transports (%s vs %s). While only "+
				"the first daemon was up it answered %s and did NOT answer %s; session-start then ran in "+
				"the second project, whose own daemon answered admin.ping (mode=%s hot=%s), and each "+
				"project carries its own .qompack",
			p.Root, lowerPath, hashAsCreated, hashLower, addr.Path, lowerAddr.Path,
			addr.Path, lowerAddr.Path, secondResp.Mode, hotName(secondResp.Hot))
	}

	rec.Outcome = OutcomeVerified
	rec.Reason = "mixed-case project root, resolved through the payload cwd, with the product " +
		"exercised at both spellings"
	rec.Detail = detail
	writeRecord(t, rec)
}

// hotName renders ipc.HotPathMode as the submode's name. The type is a uint8 with no String
// method, so `%v` prints `0` — a number that tells a reader of an evidence record nothing.
func hotName(h ipc.HotPathMode) string {
	switch h {
	case ipc.HotSync:
		return "sync"
	case ipc.HotSpool:
		return "spool"
	default:
		return fmt.Sprintf("HotPathMode(%d)", uint8(h))
	}
}

// TestPlatform_PluginRootWithSpacesAndUnicode drives the bundle from an installation directory
// whose name contains BOTH a space and non-ASCII characters — packaging/README.md §7's open
// questions 2 and 3 in one run — with CLAUDE_PLUGIN_ROOT pointing at it, exactly as a host sets it.
//
// Two things are asserted that no other case can. `self-test --json`'s plugin.root_resolves check
// must observe "resolved": that check is the product's own statement that ${CLAUDE_PLUGIN_ROOT}
// expanded to a real binary (internal/contract/assertions.go), and it is the only place the product
// reads the variable at all. And the bundle directory must be BYTE-IDENTICAL after every hook has
// run: 00-ARCHITECTURE.md §3.3 puts product data under <project>/.qompack/ and ~/.qompack/ and
// nowhere else, and the plugin install directory is replaced wholesale on upgrade, so anything
// written there is data the next version silently destroys.
func TestPlatform_PluginRootWithSpacesAndUnicode(t *testing.T) {
	src := assembledBundle(t)
	rec := newRecord(t, "path-plugin-root-spaces-unicode")

	base := tempBase(t)
	pluginRoot := filepath.Join(base, pluginRootName)
	require.NoError(t, copyTree(src.Dir, pluginRoot), "copying the bundle to %q", pluginRoot)

	installed := bundle{
		Dir:          pluginRoot,
		Bin:          filepath.Join(pluginRoot, "bin", "qompack"+exeSuffix()),
		Version:      src.Version,
		BinarySHA256: src.BinarySHA256,
	}
	sum, err := fileSHA256(installed.Bin)
	require.NoError(t, err, "the copied bundle must carry a runnable binary")
	require.Equal(t, src.BinarySHA256, sum, "the copy must be byte-identical to the assembled binary")

	p := newProjectAt(t, base, plainRootName)
	env := map[string]string{}
	for k, v := range p.Env {
		env[k] = v
	}
	env["CLAUDE_PLUGIN_ROOT"] = pluginRoot

	t.Cleanup(func() { shutdownIfReachable(t, p.Root) })

	beforeBundle := snapshotTree(pluginRoot)
	require.NotEmpty(t, beforeBundle, "the copied bundle must contain files to compare")

	for _, h := range hookSubcommands {
		stdout, stderr, code := run(t, installed.Bin, installed.Dir, h.argv,
			hookPayload(t, h.event, p.Root), env)
		require.Equal(t, 0, code,
			"hook %q must exit 0 from a plugin root containing a space and non-ASCII characters\nstdout:\n%s\nstderr:\n%s",
			h.event, stdout, stderr)
		parseHookOutput(t, h.event+" from "+pluginRoot, stdout)
	}

	// self-test is the ONE subcommand permitted a non-zero exit (§2.3), so its exit code is
	// recorded rather than asserted; what is asserted is the one check this case exists for.
	stdout, stderr, selfTestCode := run(t, installed.Bin, installed.Dir,
		[]string{"self-test", "--json"}, nil, env)
	report := parseSelfTest(t, stdout, stderr)
	check, found := report.check("plugin.root_resolves")
	require.True(t, found, "self-test --json must report plugin.root_resolves\nstdout:\n%s", stdout)
	require.True(t, check.OK,
		"CLAUDE_PLUGIN_ROOT=%q must expand to an existing binary; self-test observed %q",
		pluginRoot, check.Observed)
	require.Equal(t, "resolved", check.Observed,
		"plugin.root_resolves must report `resolved`, not the unset short-circuit")

	shutdownIfReachable(t, p.Root)

	afterBundle := snapshotTree(pluginRoot)
	require.Equal(t, beforeBundle, afterBundle,
		"the plugin install directory must be byte-identical after every hook: product data lives "+
			"under .qompack/, and an upgrade replaces this directory wholesale (§3.3)")

	rec.Outcome = OutcomeVerified
	rec.Reason = "plugin root containing a space and non-ASCII characters"
	rec.Detail = fmt.Sprintf(
		"CLAUDE_PLUGIN_ROOT=%q; six hooks exit 0 with parseable output; self-test --json exit %d with "+
			"plugin.root_resolves ok=%t observed=%q; %d bundle files byte-identical afterwards",
		pluginRoot, selfTestCode, check.OK, check.Observed, len(afterBundle))
	writeRecord(t, rec)
}

// selfTestCheckRow is one row of `qompack self-test --json`'s `checks` array.
type selfTestCheckRow struct {
	ID       string `json:"id"`
	OK       bool   `json:"ok"`
	Severity int    `json:"severity"`
	Expected string `json:"expected"`
	Observed string `json:"observed"`
	Detail   string `json:"detail"`
}

// selfTestReport mirrors `qompack self-test --json`'s document. It is re-declared here rather than
// exported from internal/cli because the shape is a CLI output contract this package OBSERVES: a
// shared struct would make a rename invisible to exactly the test that should catch it.
type selfTestReport struct {
	Checks []selfTestCheckRow `json:"checks"`
	Mode   string             `json:"mode"`
	Exit   int                `json:"exit"`
}

// check returns the row with the given id.
func (r selfTestReport) check(id string) (selfTestCheckRow, bool) {
	for _, c := range r.Checks {
		if c.ID == id {
			return c, true
		}
	}
	return selfTestCheckRow{}, false
}

// parseSelfTest decodes `self-test --json`'s stdout.
func parseSelfTest(t *testing.T, stdout, stderr []byte) selfTestReport {
	t.Helper()
	var rep selfTestReport
	require.NoError(t, jsonUnmarshalTrimmed(stdout, &rep),
		"self-test --json must write one JSON document\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	require.NotEmpty(t, rep.Checks, "self-test --json must report at least one check")
	return rep
}
