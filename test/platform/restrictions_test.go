package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// daemonOffEnvKey switches the daemon off through the configuration ENVIRONMENT layer
// (QOMPACK_<SEC>__<KEY>, internal/config/load.go's envVarName) rather than through a state.bin.
//
// It has to be the environment for the read-only-project-root case specifically: writing a
// state.bin would require creating `.qompack/` first, and whether the product survives NOT being
// able to create it is the whole of what that case asks. doHook ANDs the persisted state with
// runtime.daemon.enabled before its preSend seam runs (internal/cli/hookclient.go), and
// ensureDaemonRunning returns early on the same flag, so this suppresses both the session-start
// spawn and every client dial.
//
// What that costs, stated plainly in every record below: these cases cover the hook adapter's exit
// contract and its degradation path under a managed restriction, not the daemon behind it.
const daemonOffEnvKey = "QOMPACK_RUNTIME__DAEMON__ENABLED"

// restrictionEnv is a project's environment with the daemon switched off.
func restrictionEnv(p project) map[string]string {
	env := map[string]string{daemonOffEnvKey: "false"}
	for k, v := range p.Env {
		env[k] = v
	}
	return env
}

// requireDenyOrSkip proves the deny bit down before anything is asserted on top of it, and records
// a skip rather than a failure when the host will not honour one.
//
// A process that can write through a deny — root on POSIX, a privileged token on Windows — cannot
// express "read-only directory" at all, and a case that ran anyway would be the unrestricted run
// wearing a restriction's name. That is not a product failure and must not be recorded as one.
func requireDenyOrSkip(t *testing.T, rec Record, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if !denyBites(t, d) {
			skipRecorded(t, rec, "a file can still be created under "+d+" after a deny, so this host "+
				"cannot present the product with a read-only directory (a privileged process ignores "+
				"both POSIX mode bits and a deny ACE)")
		}
	}
}

// runAllHooksExpectZero drives the six hooks and asserts §13 invariant 6 for each, returning the
// combined stderr so a caller can look for degradation evidence in it.
func runAllHooksExpectZero(t *testing.T, b bundle, p project, env map[string]string, what string) string {
	t.Helper()
	var stderrAll strings.Builder
	for _, h := range hookSubcommands {
		stdout, stderr, code := run(t, b.Bin, b.Dir, h.argv, hookPayload(t, h.event, p.Root), env)
		require.Equal(t, 0, code,
			"hook %q must exit 0 with %s (§13 invariant 6: hooks exit 0, always)\nstdout:\n%s\nstderr:\n%s",
			h.event, what, stdout, stderr)
		require.True(t, emptyOrParseableJSON(stdout),
			"hook %q must write empty or parseable JSON with %s, got:\n%s", h.event, what, stdout)
		parseHookOutput(t, h.event+" ("+what+")", stdout)
		stderrAll.Write(stderr)
	}
	return stderrAll.String()
}

// TestPlatform_ReadOnlyProjectRoot is the managed-restriction row where the product may not create
// its own store at all: a project directory an administrator, a repository policy or a mounted
// volume has made non-writable.
//
// The store does not exist yet when the deny lands, so `.qompack/` cannot be created. §13
// invariant 6 does not bend for that — the hooks still exit 0 and still write output a host can
// consume, because a hook that fails takes the user's turn with it.
func TestPlatform_ReadOnlyProjectRoot(t *testing.T) {
	b := assembledBundle(t)
	rec := newRecord(t, "restrict-readonly-project-root")

	p := newProject(t, plainRootName)
	require.NoError(t, denyWrites(p.Root), "denying writes on %s", p.Root)
	requireDenyOrSkip(t, rec, p.Root)

	env := restrictionEnv(p)
	stderr := runAllHooksExpectZero(t, b, p, env, "a read-only project root")

	_, statErr := os.Stat(paths.Long(paths.Of(p.Root).Dot))
	created := statErr == nil

	rec.Outcome = OutcomeVerified
	rec.Reason = "all six hooks exit 0 with consumable output on a read-only project root"
	rec.Detail = fmt.Sprintf(
		"deny applied to %q before any store existed; .qompack present afterwards=%t; daemon "+
			"suppressed through %s=false, so this row covers the hook adapter's exit contract, not the "+
			"daemon; hook stderr was %d bytes",
		p.Root, created, daemonOffEnvKey, len(stderr))
	writeRecord(t, rec)
}

// TestPlatform_ReadOnlyQompackDir is the row where the store EXISTS and then becomes non-writable:
// the case §12.3's "spool write fails — drop the event, count it, Loud once" was written for.
//
// Two things are asserted and one is measured. The hooks must still exit 0 with consumable output
// — that is the contract. And whatever degradation evidence the shipped code actually produces is
// SEARCHED FOR across every place it could land, and recorded. It is searched for rather than
// asserted because the brief for this commit is explicit that the observable must be found in the
// existing code, not invented: if the only evidence of a dropped event is a counter in a registry
// that dies with the process and a Loud line whose sink is inside the very directory that cannot
// be written, then there is nothing durable to assert and THAT is the finding.
func TestPlatform_ReadOnlyQompackDir(t *testing.T) {
	b := assembledBundle(t)
	rec := newRecord(t, "restrict-readonly-qompack-dir")

	p := newProject(t, plainRootName)
	l := paths.Of(p.Root)
	require.NoError(t, paths.EnsureLayout(l), "creating the layout before denying writes to it")

	// One ordinary hook first, so the store is not merely laid out but has been written to, and the
	// before/after comparison below is against a live store rather than an empty one.
	env := restrictionEnv(p)
	stdout, stderr, code := run(t, b.Bin, b.Dir, []string{"observe", "tool"},
		hookPayload(t, "PostToolUse", p.Root), env)
	require.Equal(t, 0, code, "the warm-up hook must exit 0\nstdout:\n%s\nstderr:\n%s", stdout, stderr)

	before := degradationEvidence(t, p.Root)
	require.NoError(t, denyWrites(l.Dot), "denying writes on %s", l.Dot)
	requireDenyOrSkip(t, rec, l.Dot, l.Spool)

	hookStderr := runAllHooksExpectZero(t, b, p, env, "a read-only .qompack directory")
	after := degradationEvidence(t, p.Root)

	found := evidenceDelta(before, after)
	if strings.TrimSpace(hookStderr) != "" {
		found = append(found, fmt.Sprintf("hook stderr (%d bytes)", len(hookStderr)))
	}
	sort.Strings(found)

	// Not every change is evidence OF THE DROP. A fingerprint moving under records/ or
	// state/config-violations.json says something was written; only the spool line, the Loud sinks
	// or the hook's own stderr says an event was lost, which is the fact §12.3 requires to survive.
	// Crediting any delta at all would let an unrelated write stand in for the thing that is
	// missing.
	namesTheDrop := intersect(found, dropNamingObservables)

	var outcome Outcome
	var reason, detail string
	switch {
	case len(namesTheDrop) > 0:
		outcome = OutcomeVerified
		reason = "a read-only .qompack leaves durable evidence that names the dropped event"
		detail = fmt.Sprintf("drop-naming observables that changed under the deny: %v (all changes: %v)",
			namesTheDrop, found)
	case len(found) > 0:
		outcome = OutcomeFailed
		reason = "a read-only .qompack changed something, but nothing that names the dropped event"
		detail = fmt.Sprintf(
			"observables that changed: %v; none of the drop-naming ones (%v) did. A write that is not "+
				"about the lost event cannot discharge §12.3's requirement that the drop be visible. "+
				"OWNER: internal/cli + internal/ipc (SP-17 Task 6 to route, Task 5 if it needs a "+
				"doctor-visible surface); test/platform records this and does not fix it",
			found, dropNamingObservables)
		t.Logf("FINDING (returned, not fixed): %s", reason)
	default:
		// The finding. Recorded with its owner; not fixed here, and not asserted away.
		outcome = OutcomeFailed
		reason = "a read-only .qompack produces NO durable degradation evidence a later process can read"
		detail = fmt.Sprintf(
			"all six hooks exited 0 with consumable output, but none of %v changed and stderr was empty. "+
				"§12.3's drop path Louds through internal/cli's hookLogger, whose sink is "+
				"<root>/.qompack/logs — inside the directory that cannot be written — and counts "+
				"l0_dropped in an obs.Registry that dies with the hook process, so a degradation that "+
				"§13 invariant 10 calls loud is invisible to everything outside that one process. "+
				"OWNER: internal/cli + internal/ipc (SP-17 Task 6 to route, Task 5 if it needs a "+
				"doctor-visible surface); test/platform records this and does not fix it",
			evidenceNames(before))
		t.Logf("FINDING (returned, not fixed): %s", reason)
	}

	rec.Outcome = outcome
	rec.Reason = reason
	rec.Detail = detail + fmt.Sprintf("; deny applied to %q after the layout existed; daemon suppressed through %s=false",
		l.Dot, daemonOffEnvKey)
	writeRecord(t, rec)
}

// TestPlatform_ReadOnlyBundleDir is the row where the plugin install directory itself is
// non-writable, which is the NORMAL state of a managed install: a host that unpacks a plugin into
// a shared location and then removes write permission, or a `Program Files` install.
//
// It is the enforcement side of §3.3's "product data lives in <project>/.qompack/ and ~/.qompack/,
// never the plugin install directory": if anything in the product ever wrote beside its own
// binary, this is where it would surface, as a failure rather than as silent data loss on the next
// upgrade.
func TestPlatform_ReadOnlyBundleDir(t *testing.T) {
	src := assembledBundle(t)
	rec := newRecord(t, "restrict-readonly-bundle-dir")

	base := tempBase(t)
	installed := filepath.Join(base, pluginRootName)
	require.NoError(t, copyTree(src.Dir, installed), "copying the bundle to %q", installed)

	p := newProjectAt(t, base, plainRootName)
	env := restrictionEnv(p)
	env["CLAUDE_PLUGIN_ROOT"] = installed

	before := snapshotTree(installed)
	require.NotEmpty(t, before)
	require.NoError(t, denyWrites(installed), "denying writes on %s", installed)
	requireDenyOrSkip(t, rec, installed, filepath.Join(installed, "bin"))

	b := bundle{
		Dir:          installed,
		Bin:          filepath.Join(installed, "bin", "qompack"+exeSuffix()),
		Version:      src.Version,
		BinarySHA256: src.BinarySHA256,
	}
	stderr := runAllHooksExpectZero(t, b, p, env, "a read-only plugin install directory")

	after := snapshotTree(installed)
	require.Equal(t, before, after,
		"the plugin install directory must be byte-identical after a run, deny or no deny (§3.3)")

	rec.Outcome = OutcomeVerified
	rec.Reason = "all six hooks run from a read-only plugin install directory"
	rec.Detail = fmt.Sprintf(
		"deny applied to %q; %d bundle files byte-identical afterwards; CLAUDE_PLUGIN_ROOT set to the "+
			"denied directory; hook stderr was %d bytes; daemon suppressed through %s=false",
		installed, len(after), len(stderr), daemonOffEnvKey)
	writeRecord(t, rec)
}

// TestPlatform_UnwritableHome is the row where the user-global tier is unavailable: a locked-down
// or read-only home directory, which is ordinary on managed desktops and on CI runners that mount
// one.
//
// ~/.qompack is one of the five permitted write locations (§3.3) and the user-global configuration
// layer lives there, so losing it must degrade rather than fail: the project tier is still
// writable and the session must proceed.
func TestPlatform_UnwritableHome(t *testing.T) {
	b := assembledBundle(t)
	rec := newRecord(t, "restrict-unwritable-home")

	p := newProject(t, plainRootName)
	require.NoError(t, denyWrites(p.Home), "denying writes on %s", p.Home)
	requireDenyOrSkip(t, rec, p.Home)

	env := restrictionEnv(p)
	stderr := runAllHooksExpectZero(t, b, p, env, "an unwritable HOME")

	rec.Outcome = OutcomeVerified
	rec.Reason = "all six hooks exit 0 with an unwritable HOME"
	rec.Detail = fmt.Sprintf(
		"deny applied to %q (HOME and USERPROFILE both point there); the project tier stayed writable; "+
			"hook stderr was %d bytes; daemon suppressed through %s=false",
		p.Home, len(stderr), daemonOffEnvKey)
	writeRecord(t, rec)
}

// degradationEvidence is every place the shipped code could leave a durable trace of a §12.3 drop,
// as a name -> fingerprint map. The names are the code's own, not invented ones:
//
//   - the client spool file (internal/ipc/spool.go's `client-<pid>.ndjson`), which is where an
//     undeliverable event goes when the spool CAN be written;
//   - LOUD.log and the day log (internal/logging/logger.go), where Loud lands when a logs
//     directory exists and can be opened;
//   - the metrics directory, where an obs registry would have to persist l0.dropped for anything
//     outside the hook process to see it;
//   - state/config-violations.json (internal/cli/config.go), the one §11.3 record a hook persists.
func degradationEvidence(t *testing.T, root string) map[string]string {
	t.Helper()
	l := paths.Of(root)
	out := map[string]string{
		"spool/client-*.ndjson":        dirFingerprint(l.Spool),
		"logs/LOUD.log":                fileFingerprint(filepath.Join(l.Logs, "LOUD.log")),
		"logs/*":                       dirFingerprint(l.Logs),
		"metrics/*":                    dirFingerprint(l.Metrics),
		"state/config-violations.json": fileFingerprint(filepath.Join(l.State, "config-violations.json")),
		"records/*":                    dirFingerprint(l.Records),
	}
	return out
}

// dropNamingObservables are the degradationEvidence keys whose movement would actually say an
// EVENT WAS LOST, as opposed to merely that something was written.
//
// The spool file is where an undeliverable event goes when the spool can be written; LOUD.log and
// the day log are where §12.3's "Loud once" lands when a logs directory can be opened; the hook's
// own stderr is the last channel that survives when neither can. records/, metrics/ and
// state/config-violations.json are deliberately absent: a change there proves the process was
// alive, not that it told anyone what it lost.
var dropNamingObservables = []string{
	"spool/client-*.ndjson",
	"logs/LOUD.log",
	"logs/*",
	"hook stderr",
}

// intersect returns the members of got that match one of want, treating a "hook stderr (N bytes)"
// entry as the "hook stderr" family.
func intersect(got, want []string) []string {
	var out []string
	for _, g := range got {
		for _, w := range want {
			if g == w || strings.HasPrefix(g, w+" ") {
				out = append(out, g)
				break
			}
		}
	}
	return out
}

// evidenceNames is degradationEvidence's key set, sorted, for a record that has to say what was
// looked at as well as what was found.
func evidenceNames(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// evidenceDelta returns the observables whose fingerprint changed.
func evidenceDelta(before, after map[string]string) []string {
	var out []string
	for k, a := range after {
		if before[k] != a {
			out = append(out, k)
		}
	}
	return out
}

// dirFingerprint summarises a directory as "<entry count>:<total bytes>", which changes whenever a
// file is added to it or grows. It deliberately does not hash contents: the day log carries a
// timestamp on every line, so a hash would report a change for a write that happened before the
// deny as readily as for one after it.
func dirFingerprint(dir string) string {
	entries, err := os.ReadDir(paths.Long(dir))
	if err != nil {
		return "absent"
	}
	var total int64
	for _, e := range entries {
		if info, infoErr := e.Info(); infoErr == nil {
			total += info.Size()
		}
	}
	return fmt.Sprintf("%d:%d", len(entries), total)
}

// fileFingerprint summarises one file as its size, or "absent".
func fileFingerprint(p string) string {
	fi, err := os.Stat(paths.Long(p))
	if err != nil {
		return "absent"
	}
	return fmt.Sprintf("%d", fi.Size())
}
