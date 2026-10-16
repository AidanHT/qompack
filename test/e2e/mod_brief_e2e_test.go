package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/modwire"
	"github.com/qompack/qompack/internal/paths"
)

// modBriefArgv is `qompack mod brief` with both mod switches on for the invocation.
var modBriefArgv = []string{
	"mod", "brief", "--set", "runtime.mod.enabled=true", "--set", "runtime.mod.steering=true",
}

// modProject is a temp repository for the mod rows: a root with a .git marker and a store, a
// subdirectory inside it, and a home of its own. Its children resolve the root from the request's
// cwd, as the mod's do: QOMPACK_PROJECT_ROOT is cleared, and HOME and USERPROFILE name the home.
type modProject struct {
	root, sub, home string
}

func newE2EModProject(t *testing.T) modProject {
	t.Helper()
	base := t.TempDir()
	p := modProject{
		root: filepath.Join(base, "repo"),
		home: filepath.Join(base, "home"),
	}
	p.sub = filepath.Join(p.root, "pkg", "deep")
	for _, d := range []string{filepath.Join(p.root, ".git"), p.sub, p.home} {
		require.NoError(t, os.MkdirAll(d, 0o700))
	}
	require.NoError(t, paths.EnsureLayout(paths.Of(p.root)))
	return p
}

func (p modProject) env() map[string]string {
	return map[string]string{"QOMPACK_PROJECT_ROOT": "", "HOME": p.home, "USERPROFILE": p.home}
}

// brief runs `qompack mod brief` for cwd and returns its one response and how long the process took.
func (p modProject) brief(t *testing.T, bin, cwd string) (modwire.BriefResponse, time.Duration) {
	t.Helper()
	req, err := modwire.Encode(modwire.BriefRequest{
		V: modwire.Version, SessionID: "e2e-mod-brief", CWD: cwd, HostVersion: modwire.MinHostVersion,
		Trigger: modwire.TriggerAuto,
	})
	require.NoError(t, err)
	began := time.Now()
	stdout, stderr, code := Run(t, bin, modBriefArgv, req, p.env())
	took := time.Since(began)
	require.Equal(t, 0, code, "exit 0 on every handled outcome; stderr:\n%s", stderr)
	require.Equal(t, 1, bytes.Count(stdout, []byte("\n")), "exactly one response line: %q", stdout)
	var r modwire.BriefResponse
	require.NoError(t, json.Unmarshal(stdout, &r), "stdout: %q", stdout)
	require.NoError(t, r.Validate(modwire.BriefMaxChars), "stdout: %q", stdout)
	return r, took
}

// records reads the session's brief-outcome log in the root's store.
func (p modProject) records(t *testing.T) []modwire.BriefRecord {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(paths.Of(p.root).State, "mod-brief-e2e-mod-brief.jsonl"))
	require.NoError(t, err)
	var out []modwire.BriefRecord
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		r, err := modwire.ParseBriefRecord(sc.Bytes())
		require.NoError(t, err, "line %q", sc.Text())
		out = append(out, r)
	}
	return out
}

// requireNoSpool asserts no client spool file exists for the project: a brief is never spooled.
func (p modProject) requireNoSpool(t *testing.T) {
	t.Helper()
	files, err := ipc.SpoolFiles(paths.Of(p.root).Spool)
	require.NoError(t, err)
	require.Empty(t, files, "a brief is never spooled")
}

// TestE2E_ModBriefColdThenServed drives the real binary the way the mod does, from a session root in
// a subdirectory of the repository. With no daemon the brief answers cold at once and starts one;
// once that daemon listens, the next brief is answered by it. Both land in the repository root's
// store, each as one record line, and nothing is spooled.
func TestE2E_ModBriefColdThenServed(t *testing.T) {
	bin := Build(t)
	p := newE2EModProject(t)
	// The cold brief starts a real detached daemon; shut it down before the temp tree goes.
	t.Cleanup(func() { e2eShutdownIfReachable(t, p.root) })

	r, took := p.brief(t, bin, p.sub)
	require.False(t, r.OK)
	require.Equal(t, modwire.ReasonCold, r.Reason)
	require.Less(t, took, time.Duration(modwire.TimeoutMs)*time.Millisecond,
		"cold returns at once, inside the mod's timeout, without waiting for the daemon")
	recs := p.records(t)
	require.Len(t, recs, 1)
	require.Equal(t, modwire.OutcomeCold, recs[0].Outcome)
	require.Less(t, recs[0].MS, int64(modwire.TimeoutMs))
	p.requireNoSpool(t)
	require.NoDirExists(t, filepath.Join(p.sub, ".qompack"), "the subdirectory resolves to the repository root")

	addr, err := ipc.Resolve(p.root)
	require.NoError(t, err)
	deadline := time.Now().Add(e2eLazySpawnSettleBound)
	for !ipc.Probe(addr, e2eProbeTimeout) {
		require.True(t, time.Now().Before(deadline), "the daemon the cold brief started never listened")
		time.Sleep(e2eLazySpawnSettleTick)
	}

	r, _ = p.brief(t, bin, p.sub)
	require.True(t, r.OK, "the daemon answers the brief: %+v", r)
	recs = p.records(t)
	require.Len(t, recs, 2)
	require.Equal(t, modwire.OutcomeServed, recs[1].Outcome)
	require.Equal(t, modwire.CharCount(r.Instructions), recs[1].Chars)
	require.LessOrEqual(t, recs[0].TS, recs[1].TS)
	require.Greater(t, recs[1].TS, core.UnixMilli(0))
	p.requireNoSpool(t)
}

// TestE2E_ModBriefFromTheHomeDirectoryIsRefused: a session root that resolves to the home directory
// is refused and nothing is written there or anywhere else, and no daemon is started for it.
func TestE2E_ModBriefFromTheHomeDirectoryIsRefused(t *testing.T) {
	bin := Build(t)
	p := newE2EModProject(t)
	// The user-global layer's directory, which a store at the home root would collide with.
	require.NoError(t, os.MkdirAll(filepath.Join(p.home, ".qompack"), 0o700))
	before := treeSnapshot(t, p.home, p.root)

	r, _ := p.brief(t, bin, p.home)
	require.False(t, r.OK)
	require.Equal(t, modwire.ReasonRefused, r.Reason)

	require.Equal(t, before, treeSnapshot(t, p.home, p.root), "a refused brief writes nothing")
	addr, err := ipc.Resolve(p.home)
	require.NoError(t, err)
	require.False(t, ipc.Probe(addr, e2eProbeTimeout), "no daemon is started for the home root")
}

// treeSnapshot lists every path under the roots with its size and modification time.
func treeSnapshot(t *testing.T, roots ...string) map[string]string {
	t.Helper()
	got := map[string]string{}
	for _, root := range roots {
		require.NoError(t, filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			got[path] = info.ModTime().String() + "|" + info.Mode().String()
			if !info.IsDir() {
				got[path] += "|" + strconv.FormatInt(info.Size(), 10)
			}
			return nil
		}))
	}
	return got
}
