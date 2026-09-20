package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// TestHookLogger_FallsBackToTheUserLevelLogDirectory is finding F-2.
//
// test/platform made a project's .qompack directory read-only underneath a live session and found
// that the product left NO durable evidence of the degradation: logging.New failed, the hook logger
// fell straight to logging.Nop, and the only trace — ipc.Client's l0.dropped counter — died with the
// hook process. §13 invariant 10 says degradation is loud, and this is where "loud" has to survive
// the process that emitted it.
//
// The unwritable log directory is simulated portably: logs/ exists, but a DIRECTORY sits where
// today's day log file must be opened, so logging.New fails at exactly the step it fails at under a
// deny-ACE — it cannot open its sink — without needing icacls or a non-root POSIX user.
func TestHookLogger_FallsBackToTheUserLevelLogDirectory(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	l := paths.Of(root)
	require.NoError(t, os.MkdirAll(l.Logs, 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(l.Logs, dayLogFileName()), 0o700))

	log := newHookLoggerWithHome(root, home)
	log.Loud("store: object write refused", "err", "read-only")
	hl, ok := log.(*hookLogger)
	require.True(t, ok)
	hl.closeSink()

	homeLogs := filepath.Join(paths.Global(home), "logs")
	entries, err := os.ReadDir(homeLogs)
	require.NoError(t, err, "the user-level fallback directory must have been created and used")
	require.NotEmpty(t, entries, "the fallback sink must have written something")

	var found bool
	for _, e := range entries {
		b, readErr := os.ReadFile(filepath.Join(homeLogs, e.Name()))
		require.NoError(t, readErr)
		if strings.Contains(string(b), "store: object write refused") {
			found = true
		}
	}
	require.True(t, found, "the Loud line must be durable under %s, not only in the process ring", homeLogs)
	require.FileExists(t, filepath.Join(homeLogs, "LOUD.log"),
		"a Loud through the fallback still writes the append-only LOUD.log")
}

// TestHookLogger_PrefersTheProjectLogDirectory keeps F-2's fallback from becoming the normal path:
// an ordinary project must still log into its own store and must not create anything under home.
func TestHookLogger_PrefersTheProjectLogDirectory(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	require.NoError(t, paths.EnsureLayout(paths.Of(root)))

	log := newHookLoggerWithHome(root, home)
	log.Loud("store: object write refused", "err", "none")
	hl, ok := log.(*hookLogger)
	require.True(t, ok)
	hl.closeSink()

	_, err := os.Stat(paths.Global(home))
	require.True(t, os.IsNotExist(err), "an ordinary project must not reach for the user-level sink")

	entries, readErr := os.ReadDir(paths.Of(root).Logs)
	require.NoError(t, readErr)
	require.NotEmpty(t, entries, "the project's own log directory is where this lands")
}

// TestHookLogger_NeverCreatesStateForAProjectThatNeverOptedIn: no .qompack, no fallback. A hook must
// not conjure a user-level log directory out of a project it was never installed for.
func TestHookLogger_NeverCreatesStateForAProjectThatNeverOptedIn(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()

	log := newHookLoggerWithHome(root, home)
	log.Loud("store: object write refused", "err", "none")
	hl, ok := log.(*hookLogger)
	require.True(t, ok)
	hl.closeSink()

	_, err := os.Stat(paths.Global(home))
	require.True(t, os.IsNotExist(err), "a project with no .qompack store has not degraded")
	_, err = os.Stat(paths.Of(root).Dot)
	require.True(t, os.IsNotExist(err), "and nothing was created in the project either")
}

// dayLogFileName is internal/logging's own day-log filename for today, rebuilt here rather than
// exported: the test needs to occupy that exact path to make logging.New fail on an EXISTING
// directory, which is the shape a read-only .qompack has.
func dayLogFileName() string {
	return "qompack-" + time.Now().UTC().Format("20060102") + ".log"
}
