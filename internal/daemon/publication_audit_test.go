package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// These tests pin the daemon-side wiring of the publication accounting: the narrow, non-sensitive
// report, and the single LOUD announcement main calls at startup. They build a real *FSStore behind
// the daemon's Services so the type assertion to store.PublicationAuditor is exercised for real, and
// capture LOUD output through the process-wide observer.

// loudCapture collects every Loud message fired while it is attached. AttachLoudObserver is
// process-wide and Loud can fire from any goroutine, so the slice is mutex-guarded.
type loudCapture struct {
	mu   sync.Mutex
	msgs []string
}

func (c *loudCapture) attach(t *testing.T) {
	t.Helper()
	logging.AttachLoudObserver(func(msg string, _ ...any) {
		c.mu.Lock()
		c.msgs = append(c.msgs, msg)
		c.mu.Unlock()
	})
	t.Cleanup(func() { logging.AttachLoudObserver(nil) })
}

func (c *loudCapture) contains(sub string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, m := range c.msgs {
		if strings.Contains(m, sub) {
			return true
		}
	}
	return false
}

// newAuditDaemon builds a minimal daemon over a real store rooted at a fresh temp project. Only the
// three fields the accounting helpers touch (svc.Store, log, m) are set; New is deliberately not
// used, so the test depends on nothing but the seam under test.
func newAuditDaemon(t *testing.T) (*daemon, string, *obs.Registry) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "project")
	home := filepath.Join(base, "home")
	for _, d := range []string{root, home} {
		require.NoError(t, os.MkdirAll(paths.Long(d), 0o700))
	}
	// Redirect HOME so the store's default token estimator does not read the developer's own
	// ~/.qompack (§13 invariant 7), the same guard newProject uses in internal/store's tests.
	t.Setenv("QOMPACK_HOME", filepath.Join(home, ".qompack"))
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	m := obs.New(core.SystemClock())
	st, err := store.Open(root, config.Defaults(), store.Deps{Log: logging.Nop(), Metrics: m, Clock: core.SystemClock()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	d := &daemon{svc: &Services{Store: st}, log: logging.Nop(), m: m}
	return d, root, &m
}

// TestLoudPublicationGaps_AnnouncesAnUnpublishedCapture pins the whole startup path: a real
// stage-one observe.tool sidecar with no reference is found, reported, LOUD-announced and counted.
func TestLoudPublicationGaps_AnnouncesAnUnpublishedCapture(t *testing.T) {
	d, root, mp := newAuditDaemon(t)
	m := *mp

	var loud loudCapture
	loud.attach(t)

	// A genuine gap: a tool delivery with an ok outcome and durable bytes, still unpublished
	// (fsck calibration rule 2). Without the outcome and bytes this would be legitimately unpublished.
	id := core.ObservationID(core.HashBytes("daemon.audit.obs", []byte("stage-one")).String())
	require.NoError(t, store.WriteCaptureSidecar(root, store.CaptureSidecar{
		ObservationID: id, Session: "sess", Op: "observe.tool", Published: false,
		Outcome: core.OutcomeOK, Bytes: []byte("captured tool result"),
	}))

	rep := d.LoudPublicationGaps(context.Background())

	require.True(t, rep.Observed, "a real *FSStore must be audited, not skipped")
	require.True(t, rep.HasGaps(), "an unpublished observe.tool capture is a gap")
	require.Equal(t, 1, rep.UnpublishedCaptures)
	require.True(t, loud.contains("unpublished captures or unindexed objects"),
		"a gap must be announced on LOUD.log")
	require.Equal(t, int64(1), m.Counter(counterPublicationUnpublishedCaptures).Value(),
		"the unpublished-capture counter must carry the gap")
}

// TestLoudPublicationGaps_QuietWhenClean asserts a healthy project is observed, gapless, complete and
// does NOT put a line on LOUD.log — the announcement is reserved for something worth an operator's
// attention.
func TestLoudPublicationGaps_QuietWhenClean(t *testing.T) {
	d, _, _ := newAuditDaemon(t)

	var loud loudCapture
	loud.attach(t)

	rep := d.LoudPublicationGaps(context.Background())

	require.True(t, rep.Observed)
	require.False(t, rep.HasGaps())
	require.False(t, rep.Incomplete)
	require.False(t, loud.contains("publication accounting"),
		"a clean, complete pass must stay off LOUD.log")
	require.False(t, loud.contains("unpublished captures"),
		"a clean pass must not announce a gap it did not find")
}

// TestAccountPublicationGaps_UnobservedForNonAuditorStore asserts a store the daemon cannot audit
// yields Observed:false rather than a confident clean report — the RefCounter/Quarantiner pattern.
func TestAccountPublicationGaps_UnobservedForNonAuditorStore(t *testing.T) {
	d := &daemon{svc: &Services{Store: nil}, log: logging.Nop(), m: obs.New(core.SystemClock())}

	var loud loudCapture
	loud.attach(t)

	rep := d.LoudPublicationGaps(context.Background())

	require.False(t, rep.Observed,
		"a store that exposes no accounting capability must not be reported as clean")
	require.False(t, rep.HasGaps())
	require.False(t, loud.contains("publication accounting"),
		"an unobserved store emits no announcement at all")
}
