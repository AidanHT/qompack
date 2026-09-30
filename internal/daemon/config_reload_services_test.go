package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// A config reload reaches every service that reads the setting it changed (V6 close-out D49). The
// candidate 4 live re-run's UAT-05 logged "config reloaded changed=[runtime.rehydrate.maxTokens
// runtime.rehydrate.minTokens]" and went on rehydrating at its startup budget until a restart,
// because reload.go replaced the daemon's own copy of the configuration while the rehydrate service
// read the copy it was wired with. UAT-09 met the same class with eliminations.staleResponse and
// already_tried. Each test here changes project config.json under a constructed daemon, reloads the
// way the idle tick and the session.start route do, and requires the service to act on the new value.

// reloadHarness is a daemon built through New from an Options its test wired, with a private project
// and home.
type reloadHarness struct {
	d    *daemon
	o    *Options
	root string
}

// newReloadHarness wires o through wire (nil for none), constructs the daemon and points its reload
// at a private home, so no test reads the developer's own ~/.qompack.
func newReloadHarness(t *testing.T, cfg config.Config, wire func(*Options)) *reloadHarness {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "project")
	home := filepath.Join(base, "home")
	for _, dir := range []string{root, home} {
		require.NoError(t, os.MkdirAll(paths.Long(dir), 0o700))
	}
	t.Setenv("QOMPACK_HOME", filepath.Join(home, ".qompack"))
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))

	o := NewOptions(root, cfg)
	o.Log = logging.Nop()
	if wire != nil {
		wire(&o)
	}
	d, err := New(o)
	require.NoError(t, err)
	dd, ok := d.(*daemon)
	require.True(t, ok)
	t.Cleanup(func() {
		_ = dd.ing.Close()
		dd.owned.closeAll(logging.Nop())
	})
	dd.cfgEnv = config.Env{ProjectRoot: root, HomeDir: home, Getenv: func(string) string { return "" }}
	return &reloadHarness{d: dd, o: &o, root: root}
}

// reload writes body as the project's config.json and reloads it, returning the keys the reload
// reported as changed.
func (h *reloadHarness) reload(t *testing.T, body string) []string {
	t.Helper()
	p := configJSONPath(h.root)
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(p)), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(p), []byte(body), 0o600))
	changed, err := h.d.reloadConfig(context.Background(), h.d.cfgEnv, true)
	require.NoError(t, err)
	return changed
}

// TestConfigReload_ReachesTheRehydrator is UAT-05's finding: a reloaded rehydration budget is the
// budget the next compact rehydration is built under.
func TestConfigReload_ReachesTheRehydrator(t *testing.T) {
	var svc observer.Rehydrator
	h := newReloadHarness(t, testConfig(), func(o *Options) { svc = WireRehydrator(o) })

	changed := h.reload(t, `{"runtime":{"rehydrate":{"minTokens":150,"maxTokens":150}}}`)
	require.Contains(t, changed, "runtime.rehydrate.maxTokens")

	const sess = core.SessionID("sess-reload-rehydrate")
	_, err := svc.OnCompact(context.Background(), hookio.Event{SessionID: sess, Source: "compact"})
	require.NoError(t, err)
	b, err := os.ReadFile(paths.Long(filepath.Join(paths.Of(h.root).State, "rehydrate-"+string(sess)+".json")))
	require.NoError(t, err, "the rehydration records its state")
	var st struct {
		Budget core.Tokens `json:"budget"`
	}
	require.NoError(t, json.Unmarshal(b, &st))
	require.EqualValues(t, 150, st.Budget, "the rehydration must be built under the reloaded budget")
}

// TestConfigReload_ReachesTheEliminationLedger: the ledger applies eliminations.staleResponse itself
// (Query drops a stale record's detail under "drop"), so a reload must reach the open ledger too.
func TestConfigReload_ReachesTheEliminationLedger(t *testing.T) {
	cfg := testConfig()
	cfg.Eliminations.RequireEvidence = false
	h := newReloadHarness(t, cfg, func(o *Options) { WireRehydrator(o) })
	l := h.o.OpenLedger()
	require.NotNil(t, l, "the ledger opens")
	ctx := negknow.WithCaller(context.Background(), negknow.Caller{Session: "sess-reload-ledger"})
	id, err := l.Record(ctx, negknow.Record{
		Target: "export delimiter", Approach: "semicolon", Reason: "the user corrected it",
		Scope: negknow.ScopeSession, Status: negknow.StatusActive, Source: negknow.SourceMCP,
	})
	require.NoError(t, err)
	require.NoError(t, l.MarkStale(ctx, []string{id}, []string{"reports.py"}))
	ans, err := l.Query(ctx, "export delimiter", "semicolon", negknow.ScopeSession)
	require.NoError(t, err)
	require.Equal(t, negknow.AnswerStale, ans.State, "flag, as opened")

	changed := h.reload(t, `{"eliminations":{"requireEvidence":false,"staleResponse":"drop"}}`)
	require.Contains(t, changed, "eliminations.staleResponse")
	ans, err = l.Query(ctx, "export delimiter", "semicolon", negknow.ScopeSession)
	require.NoError(t, err)
	require.Equal(t, negknow.AnswerUncertain, ans.State, "drop, reloaded, without reopening the ledger")
}

// TestConfigReload_ReachesTheSessionRegistry: runtime.daemon.maxSessions is the registry's eviction
// ceiling.
func TestConfigReload_ReachesTheSessionRegistry(t *testing.T) {
	h := newReloadHarness(t, testConfig(), nil)
	require.Contains(t, h.reload(t, `{"runtime":{"daemon":{"maxSessions":3}}}`), "runtime.daemon.maxSessions")
	h.d.registry.mu.Lock()
	got := h.d.registry.maxSessions
	h.d.registry.mu.Unlock()
	require.Equal(t, 3, got)
}

// TestConfigReload_ReachesTheBreachDetector: runtime.hotPath.budgetMs and breachWindows are what the
// hot-path breach detector gates on.
func TestConfigReload_ReachesTheBreachDetector(t *testing.T) {
	h := newReloadHarness(t, testConfig(), nil)
	changed := h.reload(t, `{"runtime":{"hotPath":{"budgetMs":7,"breachWindows":2}}}`)
	require.Contains(t, changed, "runtime.hotPath.budgetMs")
	limit, need := h.d.breach.Config()
	require.Equal(t, 7*time.Millisecond, limit)
	require.Equal(t, 2, need)
}

// TestConfigReload_ReachesTheIdleController: scheduler.idle.detectAfterSeconds is when the idle
// controller (and the client-spool watcher's horizon, which is the idle drain's) calls the daemon
// idle.
func TestConfigReload_ReachesTheIdleController(t *testing.T) {
	h := newReloadHarness(t, testConfig(), nil)
	changed := h.reload(t, `{"scheduler":{"idle":{"detectAfterSeconds":30}}}`)
	require.Contains(t, changed, "scheduler.idle.detectAfterSeconds")
	require.Equal(t, 30*time.Second, h.d.idle.detectAfter())
	require.Equal(t, 30*time.Second, h.d.spool.horizonNow())
}

// TestConfigReload_ReachesTheStoreRedactor: runtime.redact is applied by the store at every Put, so
// a secret pattern a reload adds binds the next capture.
func TestConfigReload_ReachesTheStoreRedactor(t *testing.T) {
	h := newReloadHarness(t, testConfig(), func(o *Options) {
		_, err := WireObserver(o)
		require.NoError(t, err)
	})
	t.Cleanup(func() { _ = h.o.Store.Close() })
	secret := []byte("the deploy key is ZZQSECRET12345ZZ, keep it out of the store")

	before, err := h.o.Store.PutBytes(context.Background(), secret, store.PutOptions{})
	require.NoError(t, err)
	require.Zero(t, before.Redacted, "no rule matches before the reload")

	changed := h.reload(t, `{"runtime":{"redact":{"enabled":true,"patterns":["ZZQSECRET[0-9]+ZZ"]}}}`)
	require.Contains(t, changed, "runtime.redact.patterns")
	after, err := h.o.Store.PutBytes(context.Background(), secret, store.PutOptions{})
	require.NoError(t, err)
	require.Equal(t, 1, after.Redacted, "the reloaded pattern binds the next Put")
}

// TestConfigReload_ReachesTheSchedulerRuntime: a scheduler runtime wired with the live
// configuration (SchedulerRuntimeOptions.CfgFn, as the daemon's composition root wires it) acts on a
// reloaded scheduler.idle.backgroundWork at its next idle pass.
func TestConfigReload_ReachesTheSchedulerRuntime(t *testing.T) {
	h := newReloadHarness(t, testConfig(), nil)
	fx := newRTFixture(t)
	opts := fx.options()
	opts.CfgFn = h.o.CurrentCfg
	rt, err := NewSchedulerRuntime(opts)
	require.NoError(t, err)
	sr, ok := rt.(*schedRuntime)
	require.True(t, ok)
	require.True(t, sr.frontierPlannable(), "background work on, as started")

	changed := h.reload(t, `{"scheduler":{"idle":{"backgroundWork":false}}}`)
	require.Contains(t, changed, "scheduler.idle.backgroundWork")
	require.False(t, sr.frontierPlannable(), "the reloaded switch reaches the running scheduler")
}

// TestConfigReload_HoldsAKeyThatNeedsARestart: a key held by something built at start is not
// reported as applied, is not put into the live configuration (so no reader sees half of the
// change), and is named in a Loud line.
func TestConfigReload_HoldsAKeyThatNeedsARestart(t *testing.T) {
	cfg := testConfig()
	h := newReloadHarness(t, cfg, nil)
	loud := &reloadLoudLog{}
	h.d.log = loud

	changed, restart, err := func() ([]string, []string, error) {
		p := configJSONPath(h.root)
		require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(p)), 0o700))
		require.NoError(t, os.WriteFile(paths.Long(p), []byte(
			`{"sketches":{"bloom":{"capacity":5000}},"runtime":{"daemon":{"maxSessions":3}}}`), 0o600))
		return h.d.reloadConfigKeys(context.Background(), h.d.cfgEnv, true)
	}()
	require.NoError(t, err)
	require.Equal(t, []string{"sketches.bloom.capacity"}, restart)
	require.Equal(t, []string{"runtime.daemon.maxSessions"}, changed)
	require.Equal(t, cfg.Sketches.Bloom.Capacity, h.d.currentCfg().Sketches.Bloom.Capacity,
		"the running daemon keeps the capacity it sized its filter with")
	require.Equal(t, 3, h.d.currentCfg().Runtime.Daemon.MaxSessions)
	require.Len(t, loud.lines, 1)
	require.Contains(t, loud.lines[0], "needs a daemon restart")
}

// TestReloadKeyEffects_ClassifyEveryKey keeps reload_keys.go total over the configuration schema
// and free of entries that name nothing: a key added to config.Config without a decision about
// what a reload does with it fails here, not in a live session.
func TestReloadKeyEffects_ClassifyEveryKey(t *testing.T) {
	keys := flattenConfig(config.Defaults())
	require.NotEmpty(t, keys)
	for key := range keys {
		_, ok := effectOf(key)
		require.True(t, ok, "config key %q has no reload classification in reload_keys.go", key)
	}
	for _, e := range reloadKeyEffects {
		used := false
		for key := range keys {
			if key == e.prefix || strings.HasPrefix(key, e.prefix+".") {
				used = true
				break
			}
		}
		require.True(t, used, "reload_keys.go entry %q names no configuration key", e.prefix)
		require.NotEmpty(t, e.why, "entry %q must name the readers it rests on", e.prefix)
	}
}

// reloadLoudLog records Loud lines.
type reloadLoudLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *reloadLoudLog) With(...any) logging.Logger { return l }
func (l *reloadLoudLog) Debug(string, ...any)       {}
func (l *reloadLoudLog) Info(string, ...any)        {}
func (l *reloadLoudLog) Warn(string, ...any)        {}
func (l *reloadLoudLog) Error(string, ...any)       {}
func (l *reloadLoudLog) Loud(msg string, _ ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, msg)
}
