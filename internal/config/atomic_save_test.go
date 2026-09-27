package config_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/paths"
)

// atomicSaveReads and atomicSaveAttempts size the two loops below: each test keeps loading until it
// has made at least atomicSaveReads loads AND the writer has made at least atomicSaveAttempts saves,
// so neither half can finish vacuously on a loaded machine. Test sizing only, not a product bound.
// Before the shared reads (D22), 2,000 loads against a writer saving in a loop failed hundreds of
// times on Windows (plans/sdd/V6-closeout/w6-config/runs/), so the sizing is far above the rate
// the defect shows at.
const (
	atomicSaveReads    = 2000
	atomicSaveAttempts = 200
)

// atomicSaveBudgets are the two versions of the file the writer alternates between. Every load must
// answer one of them: anything else — the default, or a refusal — is the defect.
var atomicSaveBudgets = [2]int{9000, 9001}

// atomicSave is one way of replacing config.json atomically: write the whole new file, then rename it
// over the old one in a single step.
type atomicSave struct {
	name string
	save func(path string, body []byte) error
	// savesMustLand says whether a save may be refused while a load holds the file with delete
	// sharing. It is false for exactly one writer, and why is in atomicSaves.
	savesMustLand bool
}

// atomicSaves are the two renames an atomic save can use on Windows, and they differ in the one way
// that matters here.
//
//   - "posix-rename" replaces with POSIX semantics (FILE_RENAME_FLAG_POSIX_SEMANTICS), which lands
//     while other handles on the file are open provided each grants FILE_SHARE_DELETE. It is what
//     paths.WriteAtomic does, and so what every Qompack writer of a file some reader holds does.
//   - "movefileex" is os.Rename, MoveFileEx with MOVEFILE_REPLACE_EXISTING, the legacy rename many
//     editors still use. It refuses to replace a file ANY handle holds open, delete sharing or not
//     (measured: plans/sdd/V6-closeout/w6-config/runs/), so no reader can keep it from being
//     refused while the read is in flight. The editor reports that and saves again; what must not
//     happen is the hook failing. Its loads are held to the same rule as the other writer's.
var atomicSaves = []atomicSave{
	{name: "posix-rename", save: func(path string, body []byte) error {
		return paths.WriteAtomic(path, body, 0o600)
	}, savesMustLand: true},
	{name: "movefileex", save: func(path string, body []byte) error {
		tmp := path + ".save-tmp"
		if err := os.WriteFile(tmp, body, 0o600); err != nil {
			return err
		}
		return os.Rename(tmp, path)
	}},
}

// atomicSaveWriter saves .qompack/config.json in a loop, alternating between the two bodies, until
// stop is called, and counts the saves it attempted and those the filesystem refused.
type atomicSaveWriter struct {
	attempts atomic.Int64
	refused  atomic.Int64
	firstErr atomic.Value // string
	done     chan struct{}
	wg       sync.WaitGroup
}

func startAtomicSaveWriter(t *testing.T, root string, how atomicSave) *atomicSaveWriter {
	t.Helper()
	path := captureConfigPath(root)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	body := func(i int) []byte {
		return fmt.Appendf(nil, `{"checkpoint":{"budgetTokens":%d}}`, atomicSaveBudgets[i%2])
	}
	require.NoError(t, os.WriteFile(path, body(0), 0o600))

	w := &atomicSaveWriter{done: make(chan struct{})}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		for i := 1; ; i++ {
			select {
			case <-w.done:
				return
			default:
			}
			w.attempts.Add(1)
			if err := how.save(path, body(i)); err != nil {
				w.refused.Add(1)
				w.firstErr.CompareAndSwap(nil, err.Error())
			}
		}
	}()
	t.Cleanup(w.stop)
	return w
}

func (w *atomicSaveWriter) stop() {
	select {
	case <-w.done:
	default:
		close(w.done)
	}
	w.wg.Wait()
}

// loadUnderAtomicSaves runs load in a loop against a writer saving the project config atomically,
// and returns how many loads answered anything other than one of the two saved versions, with the
// first such answer for the failure message.
func loadUnderAtomicSaves(t *testing.T, how atomicSave, load func(env config.Env) (int, error)) (
	reads, bad int, first string, w *atomicSaveWriter,
) {
	t.Helper()
	env := baseEnv(t)
	w = startAtomicSaveWriter(t, env.ProjectRoot, how)
	for reads < atomicSaveReads || w.attempts.Load() < atomicSaveAttempts {
		reads++
		budget, err := load(env)
		switch {
		case err != nil:
			bad++
			if first == "" {
				first = "refused: " + err.Error()
			}
		case budget != atomicSaveBudgets[0] && budget != atomicSaveBudgets[1]:
			bad++
			if first == "" {
				first = fmt.Sprintf("budgetTokens %d: the project layer was silently dropped", budget)
			}
		}
	}
	w.stop()
	return reads, bad, first, w
}

// TestLoadForCapture_ReadsThroughAnEditorsAtomicSaves is D22's reason for existing. The hook path
// reads .qompack/config.json on every capture, and under D8 a config it cannot read refuses the
// capture outright. On Windows an ordinary os.Open carries no FILE_SHARE_DELETE, so an atomic save
// landing while a hook read the file failed one of the two: the hook's open was refused while the
// rename was finishing, or the rename was refused while the hook's handle was open. A user saving
// their config could make a hook record nothing. Every load must answer one of the two versions the
// writer saves, and none may refuse.
func TestLoadForCapture_ReadsThroughAnEditorsAtomicSaves(t *testing.T) {
	requireLoadsSurviveAtomicSaves(t, func(env config.Env) (int, error) {
		cfg, _, _, _, err := config.LoadForCapture(env)
		return cfg.Checkpoint.BudgetTokens, err
	})
}

// TestLoad_ReadsThroughAnEditorsAtomicSaves is the same race through config.Load, the loader the
// daemon's reload, `config print`, doctor and self-test use. It never refuses, so here the defect
// was quieter: a read the save made fail was taken for a missing file, and the whole project layer
// fell back to the defaults without a warning.
func TestLoad_ReadsThroughAnEditorsAtomicSaves(t *testing.T) {
	requireLoadsSurviveAtomicSaves(t, func(env config.Env) (int, error) {
		cfg, _, _, err := config.Load(env)
		return cfg.Checkpoint.BudgetTokens, err
	})
}

func requireLoadsSurviveAtomicSaves(t *testing.T, load func(env config.Env) (int, error)) {
	t.Helper()
	for _, how := range atomicSaves {
		t.Run(how.name, func(t *testing.T) {
			reads, bad, first, w := loadUnderAtomicSaves(t, how, load)
			firstErr, _ := w.firstErr.Load().(string)
			t.Logf("%d loads, %d wrong; %d saves attempted, %d refused (first: %s)",
				reads, bad, w.attempts.Load(), w.refused.Load(), firstErr)
			require.Zero(t, bad, "of %d loads under atomic saves, %d refused or lost the project layer "+
				"(first: %s)", reads, bad, first)
			if how.savesMustLand {
				require.Zero(t, w.refused.Load(), "of %d atomic saves, %d were refused while a load held "+
					"the file (first: %s)", w.attempts.Load(), w.refused.Load(), firstErr)
			}
		})
	}
}
