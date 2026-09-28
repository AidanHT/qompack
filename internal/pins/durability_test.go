package pins_test

// A pin reported kept survives a power cut (C1.6/D24, w6-ckptsync).
//
// Add and Remove append to pins/invariants.jsonl — the truth (§3.3) — and then regenerate the
// derived view pins/invariants.json through paths.ReplacePinsView, which syncs the view and its
// directory. The log line used to be appended without a sync, so a power cut after Add returned
// could keep the durable view and lose its source: replay then rebuilt the live set without the pin,
// and the next Materialize rewrote the view to match, dropping a pin the user was told was kept.
//
// pinBarriers is the store's paths.Barriers: it records each barrier, checks that the view has not
// yet changed when the log's line is synced, and keeps a POSIX worst-case model of what the barriers
// made durable — the log's bytes up to its last sync, and its name only once the pins directory has
// been synced since the log was created. powerCut rebuilds the log from that model; the view is left
// as ReplacePinsView made it, which is the worst case for the loss above.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/pins"
	"github.com/qompack/qompack/internal/testutil"
)

var errPinPowerCut = errors.New("injected power cut")

type pinBarriers struct {
	t        *testing.T
	logPath  string
	viewPath string
	// pinText is the pin the change under test adds or removes, and wasLive whether the view listed
	// it before the change. While the log's line is synced the view must still say what it said
	// before: the view derives from the log, so it may change only once the line is durable.
	pinText string
	wasLive bool
	failAt  int

	steps       []string
	viewAhead   bool
	nameDurable bool
	durBytes    int64
}

func newPinBarriers(t *testing.T, root, pinText string, wasLive bool) *pinBarriers {
	l := paths.Of(root)
	b := &pinBarriers{
		t: t, logPath: filepath.Join(l.Pins, "invariants.jsonl"),
		viewPath: filepath.Join(l.Pins, "invariants.json"), pinText: pinText, wasLive: wasLive,
	}
	if fi, err := os.Stat(paths.Long(b.logPath)); err == nil {
		b.nameDurable, b.durBytes = true, fi.Size() // a log that exists is taken as durable
	}
	return b
}

func (b *pinBarriers) barriers() paths.Barriers {
	return paths.Barriers{
		SyncFile: func(f *os.File) error {
			b.steps = append(b.steps, "file:"+filepath.Base(f.Name()))
			view, err := os.ReadFile(paths.Long(b.viewPath))
			if listed := err == nil && strings.Contains(string(view), b.pinText); listed != b.wasLive {
				b.viewAhead = true
			}
			if len(b.steps) == b.failAt {
				return errPinPowerCut
			}
			if err := f.Sync(); err != nil {
				return err
			}
			fi, err := f.Stat()
			require.NoError(b.t, err)
			b.durBytes = fi.Size()
			return nil
		},
		SyncDir: func(dir string) error {
			b.steps = append(b.steps, "dir:"+filepath.Base(dir))
			if len(b.steps) == b.failAt {
				return errPinPowerCut
			}
			if err := paths.SyncDir(dir); err != nil {
				return err
			}
			b.nameDurable = true
			return nil
		},
	}
}

// powerCut rebuilds the log as a power loss leaves it.
func (b *pinBarriers) powerCut() {
	if !b.nameDurable {
		require.NoError(b.t, os.Remove(paths.Long(b.logPath)))
		return
	}
	require.NoError(b.t, os.Truncate(paths.Long(b.logPath), b.durBytes))
}

func openPins(t *testing.T, root string) pins.Store {
	t.Helper()
	clk := testutil.NewFakeClock(time.UnixMilli(plainMillis).UTC())
	s, err := pins.OpenWith(root, logging.Nop(), obs.New(clk), clk)
	require.NoError(t, err)
	return s
}

func liveText(t *testing.T, s pins.Store, text string) bool {
	t.Helper()
	all, err := s.All(context.Background())
	require.NoError(t, err)
	for _, inv := range all {
		if inv.Text == text {
			return true
		}
	}
	return false
}

// TestPinsAReportedChangeSurvivesAPowerCut: an Add or Remove that returned nil is still true after
// a power cut, whether or not the log existed before, and the log's line was durable before the view
// changed.
func TestPinsAReportedChangeSurvivesAPowerCut(t *testing.T) {
	const earlier = "the API is versioned under /v2"
	const text = "tests use the FakeClock, never time.Now"
	for _, tc := range []struct {
		name      string
		seed      bool // pin `earlier` first, so the log exists before the change under test
		remove    bool // the change under test removes `text` instead of adding it
		wantSteps []string
	}{
		{name: "first pin creates the log", wantSteps: []string{"file:invariants.jsonl", "dir:pins"}},
		{name: "a later pin", seed: true, wantSteps: []string{"file:invariants.jsonl"}},
		{name: "a removal", seed: true, remove: true, wantSteps: []string{"file:invariants.jsonl"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			ctx := context.Background()
			s := openPins(t, root)
			if tc.seed {
				require.NoError(t, s.Add(ctx, pins.Invariant{Text: earlier}))
			}
			if tc.remove {
				require.NoError(t, s.Add(ctx, pins.Invariant{Text: text}))
			}

			b := newPinBarriers(t, root, text, tc.remove)
			pins.SetBarriersForTest(s, b.barriers())
			if tc.remove {
				require.NoError(t, s.Remove(ctx, pins.MintID(text)))
			} else {
				require.NoError(t, s.Add(ctx, pins.Invariant{Text: text}))
			}

			b.powerCut()
			re := openPins(t, root)
			require.Equal(t, !tc.remove, liveText(t, re, text),
				"a change reported done is still true after a power cut")
			if tc.seed {
				require.True(t, liveText(t, re, earlier), "the earlier pin survives too")
			}
			require.Equal(t, tc.wantSteps, b.steps,
				"the line is synced, and the pins directory too when the append created the log")
			require.False(t, b.viewAhead, "the view changed before the log line it derives from was durable")
		})
	}
}

// TestPinsAPinIsKeptWhenAnEarlierWriterLeftTheLogsNameUnsynced: a pin reported kept survives a power
// cut even when the log already exists but its name was never made durable — the project's first pin
// whose pins/ sync failed (Add returned the error, and the user pinned again), or a log another
// process created and exited before syncing (`qompack pin` is one process per pin). The pin that
// succeeds must sync pins/ itself; taking an existing log's name as durable lost it
// (w6-ckptsync review finding 1).
func TestPinsAPinIsKeptWhenAnEarlierWriterLeftTheLogsNameUnsynced(t *testing.T) {
	const first = "the API is versioned under /v2"
	const text = "tests use the FakeClock, never time.Now"
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, root string, s pins.Store)
	}{
		{name: "the first pin's directory sync failed", setup: func(t *testing.T, root string, s pins.Store) {
			failing := newPinBarriers(t, root, first, false)
			failing.failAt = 2 // the log's line syncs, then the pins/ sync fails
			pins.SetBarriersForTest(s, failing.barriers())
			require.ErrorIs(t, s.Add(context.Background(), pins.Invariant{Text: first}), errPinPowerCut)
			require.Equal(t, []string{"file:invariants.jsonl", "dir:pins"}, failing.steps)
		}},
		{name: "another process created the log", setup: func(t *testing.T, root string, _ pins.Store) {
			require.NoError(t, os.WriteFile(paths.Long(filepath.Join(paths.Of(root).Pins, "invariants.jsonl")), nil, 0o600))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			s := openPins(t, root)
			tc.setup(t, root, s)

			b := newPinBarriers(t, root, text, false)
			b.nameDurable = false // the log exists, but no barrier has made its name durable
			pins.SetBarriersForTest(s, b.barriers())
			require.NoError(t, s.Add(context.Background(), pins.Invariant{Text: text}))
			require.Equal(t, []string{"file:invariants.jsonl", "dir:pins"}, b.steps,
				"the pin syncs the directory whose entry names the log before it is reported kept")

			b.powerCut()
			require.True(t, liveText(t, openPins(t, root), text), "a pin reported kept is still kept after a power cut")
		})
	}
}

// TestPinsAnAppendWhoseSyncIsCutIsNotReported: a cut at the log's barrier fails the Add, and the view
// is not regenerated, so nothing derived claims a pin whose line is not durable.
func TestPinsAnAppendWhoseSyncIsCutIsNotReported(t *testing.T) {
	const text = "checkpoints carry no code"
	root := t.TempDir()
	s := openPins(t, root)
	b := newPinBarriers(t, root, text, false)
	b.failAt = 1
	pins.SetBarriersForTest(s, b.barriers())

	err := s.Add(context.Background(), pins.Invariant{Text: text})
	require.ErrorIs(t, err, errPinPowerCut)
	if view, verr := os.ReadFile(paths.Long(b.viewPath)); verr == nil {
		require.NotContains(t, string(view), text, "the view is not regenerated for a line that is not durable")
	}
	b.powerCut()
	require.False(t, liveText(t, openPins(t, root), text), "a cut append is not a pin")
}
