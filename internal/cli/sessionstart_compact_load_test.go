package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/paths"
)

// C1.16's load rig: the shipped composition (runDaemon, through Dispatch), the shipped hook clients
// (Dispatch of the real hook subcommands, in process), and a compaction cycle — PreCompact, then
// SessionStart(source=compact) — repeated while other goroutines keep feeding the SAME session
// PostToolUse Reads and SubagentStops. That is the shape the packaging lane's live session 2 had
// when a compact SessionStart took 10.3 s and answered {} at the hook client's 10 s reply deadline
// (plans/sdd/V6-closeout/packaging/evidence/live-s2-release-zip-autocompact/transcript-facts.json).
//
// By default it is small, and it pins the one property that does not depend on the machine: every
// compact SessionStart is answered with the rehydration or with the explicit deferred note, never
// with {} or anything else. Scaled up through the environment it is the benchmark behind C1.16's
// distributions (plans/sdd/V6-closeout/w2-lifetime/runs):
//
//	QOMPACK_C116_ROUNDS        compaction cycles (default 3)
//	QOMPACK_C116_WORKERS       goroutines feeding the session (default 2)
//	QOMPACK_C116_READ_BYTES    size of each fed Read (default 64 KiB)
//	QOMPACK_C116_FSYNC_COLOAD  extra goroutines writing, fsyncing and renaming 64 KiB files beside
//	                           the project for the whole run — the co-load a shared machine puts on
//	                           the disk the store and the history files are fsynced to (default 0)
//	QOMPACK_C116_CPU_COLOAD    extra CPU-spinning goroutines (default 0)
//
// It logs the SessionStart(compact) wall-time distribution as the hook client saw it and every
// daemon histogram from metrics/latency.json, which is what attributes the time.

// compactLoadSession is the one session every hook in the rig belongs to.
const compactLoadSession = core.SessionID("sess-c116-compact-under-ingest")

// compactLoadRig is one running daemon plus the payload builders the rig's hooks share.
type compactLoadRig struct {
	root string
	home string
	seq  atomic.Int64
}

func newCompactLoadRig(t *testing.T) (*compactLoadRig, func()) {
	t.Helper()
	return newCompactLoadRigWithConfig(t, "")
}

// newCompactLoadRigWithConfig is newCompactLoadRig with projectConfig, when it is not empty, as the
// project's .qompack/config.json, in place before the daemon and every hook client load it.
func newCompactLoadRigWithConfig(t *testing.T, projectConfig string) (*compactLoadRig, func()) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))
	if projectConfig != "" {
		writeProjectConfig(t, root, projectConfig)
	}
	r := &compactLoadRig{root: root, home: t.TempDir()}
	stop := bootstrapDaemon(t, root)
	return r, stop
}

// hook drives one shipped hook subcommand in process and returns its stdout and wall time.
func (r *compactLoadRig) hook(t *testing.T, args []string, payload map[string]any) (hookio.Output, time.Duration) {
	t.Helper()
	got, took, err := r.hookE(args, payload)
	require.NoError(t, err)
	return got, took
}

// hookE is hook for a goroutine other than the test's own: it reports instead of failing.
func (r *compactLoadRig) hookE(args []string, payload map[string]any) (hookio.Output, time.Duration, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return hookio.Output{}, 0, err
	}
	var out, errw bytes.Buffer
	start := time.Now()
	code := Dispatch(context.Background(), hookCmds(),
		append(append([]string{"qompack"}, args...), "--project", r.root),
		Env{Getenv: noEnv, Stdin: bytes.NewReader(raw), Clock: testClock(), HomeDir: r.home},
		&out, &errw)
	took := time.Since(start)
	if code != ExitOK {
		return hookio.Output{}, took, fmt.Errorf("%v exited %d: %s", args, code, errw.String())
	}
	var got hookio.Output
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		return hookio.Output{}, took, fmt.Errorf("%v stdout %q: %w", args, out.String(), err)
	}
	return got, took, nil
}

func (r *compactLoadRig) base(event string) map[string]any {
	return map[string]any{
		"hook_event_name": event,
		"session_id":      string(compactLoadSession),
		"cwd":             r.root,
		"transcript_path": filepath.Join(r.root, "transcript.jsonl"),
	}
}

// readPayload writes a fresh in-project file of size bytes and returns the PostToolUse Read that
// reports it. Every call's content is distinct, so the store really writes it.
func (r *compactLoadRig) readPayload(size int) (map[string]any, error) {
	n := r.seq.Add(1)
	rel := filepath.Join("src", fmt.Sprintf("load_%06d.go", n))
	var b strings.Builder
	for b.Len() < size {
		fmt.Fprintf(&b, "// line %d of file %d: func handler%d_%d() error { return nil }\n", b.Len(), n, n, b.Len())
	}
	body := b.String()
	abs := filepath.Join(r.root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(abs, []byte(body), 0o600); err != nil {
		return nil, err
	}
	p := r.base("PostToolUse")
	p["tool_name"] = "Read"
	p["tool_use_id"] = fmt.Sprintf("toolu_c116_%06d", n)
	p["tool_input"] = map[string]any{"file_path": abs}
	p["tool_response"] = map[string]any{"type": "text", "file": map[string]any{"filePath": abs, "content": body}}
	return p, nil
}

func (r *compactLoadRig) subagentStopPayload() map[string]any {
	n := r.seq.Add(1)
	p := r.base("SubagentStop")
	p["stop_hook_active"] = false
	p["agent_type"] = "general-purpose"
	p["last_assistant_message"] = fmt.Sprintf("summary %d: the pool timeout lives in src/pool.go", n)
	return p
}

// prime runs the startup half of a session: SessionStart(startup), the first prompt, some reads and
// a Stop, so the first compaction has a real capture to rehydrate from.
func (r *compactLoadRig) prime(t *testing.T) {
	t.Helper()
	p := r.base("SessionStart")
	p["source"] = "startup"
	r.hook(t, []string{"session-start"}, p)
	pr := r.base("UserPromptSubmit")
	pr["prompt"] = "Read notes.txt and tell me the release codeword. Keep the config port at 8443."
	r.hook(t, []string{"observe", "prompt"}, pr)
	for i := 0; i < 3; i++ {
		rp, err := r.readPayload(16 << 10)
		require.NoError(t, err)
		r.hook(t, []string{"observe", "tool"}, rp)
	}
	r.hook(t, []string{"observe", "stop"}, r.base("Stop"))
}

// compact runs one compaction cycle and returns the SessionStart(compact) output and wall time.
func (r *compactLoadRig) compact(t *testing.T) (hookio.Output, time.Duration) {
	t.Helper()
	pc := r.base("PreCompact")
	pc["trigger"] = "auto"
	r.hook(t, []string{"checkpoint"}, pc)
	ss := r.base("SessionStart")
	ss["source"] = "compact"
	return r.hook(t, []string{"session-start"}, ss)
}

// load feeds the session until ctx ends: Reads of readBytes each, a SubagentStop every fifth event.
// The first error any worker meets is kept in firstErr and stops that worker.
func (r *compactLoadRig) load(ctx context.Context, workers, readBytes int, firstErr *atomic.Value) *sync.WaitGroup {
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ctx.Err() == nil; i++ {
				var err error
				if i%5 == 4 {
					_, _, err = r.hookE([]string{"observe", "stop", "--subagent"}, r.subagentStopPayload())
				} else {
					var p map[string]any
					if p, err = r.readPayload(readBytes); err == nil {
						_, _, err = r.hookE([]string{"observe", "tool"}, p)
					}
				}
				if err != nil {
					firstErr.CompareAndSwap(nil, err)
					return
				}
			}
		}()
	}
	return &wg
}

// quantile returns the q-quantile (nearest rank) of ds, which it sorts.
func quantile(ds []time.Duration, q float64) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	i := int(q*float64(len(ds))+0.5) - 1
	if i < 0 {
		i = 0
	}
	if i >= len(ds) {
		i = len(ds) - 1
	}
	return ds[i]
}

// compactAnswer classifies one SessionStart(compact) answer: the rehydration itself, an explicit
// degraded note, or neither.
func compactAnswer(out hookio.Output) string {
	if out.HookSpecificOutput == nil {
		return "empty"
	}
	ac := out.HookSpecificOutput.AdditionalContext
	switch {
	case strings.Contains(ac, "# Qompack rehydration"):
		return "rehydration"
	case strings.HasPrefix(ac, daemon.DeferredNoteTag):
		return "deferred-note"
	default:
		return "other"
	}
}

// compactLoadReport logs the distribution and every daemon histogram.
func compactLoadReport(t *testing.T, root string, lat []time.Duration, kinds map[string]int) {
	t.Helper()
	cp := append([]time.Duration(nil), lat...)
	t.Logf("compact SessionStart wall (n=%d): p50=%s p95=%s p99=%s max=%s answers=%v",
		len(cp), quantile(cp, 0.50), quantile(cp, 0.95), quantile(cp, 0.99), quantile(cp, 1), kinds)
	b, err := os.ReadFile(filepath.Join(paths.Of(root).Metrics, "latency.json"))
	if err != nil {
		t.Logf("no metrics/latency.json: %v", err)
		return
	}
	var snap struct {
		Hists map[string]struct{ N, P50, P95, P99, Max int64 } `json:"hists"`
	}
	require.NoError(t, json.Unmarshal(b, &snap))
	names := make([]string, 0, len(snap.Hists))
	for n := range snap.Hists {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		h := snap.Hists[n]
		t.Logf("  %-34s n=%-5d p50=%-10s p95=%-10s p99=%-10s max=%s", n, h.N,
			time.Duration(h.P50), time.Duration(h.P95), time.Duration(h.P99), time.Duration(h.Max))
	}
}

// coload runs n goroutines that each write a 64 KiB file into dir, fsync it and rename it over one
// of a fixed set of names, and cpu goroutines that only spin, until ctx ends.
func coload(ctx context.Context, dir string, n, cpu int) *sync.WaitGroup {
	var wg sync.WaitGroup
	buf := make([]byte, coloadWriteBytes)
	for w := 0; w < n; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ctx.Err() == nil; i++ {
				tmp := filepath.Join(dir, fmt.Sprintf("w%d.tmp", w))
				f, err := os.Create(tmp)
				if err != nil {
					continue
				}
				_, _ = f.Write(buf)
				_ = f.Sync()
				_ = f.Close()
				_ = os.Rename(tmp, filepath.Join(dir, fmt.Sprintf("w%d-%d.dat", w, i%coloadNames)))
			}
		}()
	}
	for c := 0; c < cpu; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			x := 0
			for ctx.Err() == nil {
				for y := 0; y < coloadSpin; y++ {
					x += y
				}
			}
			_ = x
		}()
	}
	return &wg
}

// The co-load's shape: one write's size, how many names each writer rotates through, and how much
// arithmetic a spinner does between context checks.
const (
	coloadWriteBytes = 64 << 10
	coloadNames      = 64
	coloadSpin       = 1 << 20
)

// TestSessionStartCompact_UnderSameSessionIngest drives compaction cycles under concurrent
// same-session ingest (and, when asked, a disk and CPU co-load) and logs the distribution. Every
// answer must be the rehydration or the deferred note.
func TestSessionStartCompact_UnderSameSessionIngest(t *testing.T) {
	rounds := envInt("QOMPACK_C116_ROUNDS", 3)
	workers := envInt("QOMPACK_C116_WORKERS", 2)
	readBytes := envInt("QOMPACK_C116_READ_BYTES", 64<<10)
	fsyncers := envInt("QOMPACK_C116_FSYNC_COLOAD", 0)
	spinners := envInt("QOMPACK_C116_CPU_COLOAD", 0)

	r, stop := newCompactLoadRig(t)
	r.prime(t)

	ctx, cancel := context.WithCancel(context.Background())
	colo := coload(ctx, t.TempDir(), fsyncers, spinners)
	var loadErr atomic.Value
	wg := r.load(ctx, workers, readBytes, &loadErr)

	var lat []time.Duration
	kinds := map[string]int{}
	for i := 0; i < rounds; i++ {
		out, took := r.compact(t)
		lat = append(lat, took)
		kinds[compactAnswer(out)]++
		t.Logf("round %d: %s %s", i, took, compactAnswer(out))
	}
	cancel()
	wg.Wait()
	colo.Wait()
	stop()
	require.Nil(t, loadErr.Load(), "a load worker failed")
	compactLoadReport(t, r.root, lat, kinds)
	for kind, n := range kinds {
		require.Contains(t, []string{"rehydration", "deferred-note"}, kind,
			"%d compact SessionStart(s) answered with neither the rehydration nor the deferred note", n)
	}
}

func envInt(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n <= 0 {
		return def
	}
	return n
}
