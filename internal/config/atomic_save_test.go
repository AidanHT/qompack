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

// atomicSaveBudgets are the two versions of the file the writer alternates between.
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

// atomicSaveLoad is one load's answer: the project layer's checkpoint.budgetTokens as loaded, how
// many warnings and violations the loader returned, and its error.
type atomicSaveLoad struct {
	budget int
	notes  int
	err    error
}

// atomicSaveTally classifies every load made under the writer.
//
//   - versions counts the loads that answered one of the two saved versions, cleanly.
//   - absent counts the loads that answered the configuration with no project layer at all — the
//     default budget, no warning, no error — which is what each loader answers when the file does
//     not exist. Since config.Load warns for a file that exists and cannot be read
//     (TestLoad_UnreadableFileWarns), and LoadForCapture refuses one
//     (TestReadCaptureConfig_AnswersAtOnceWithoutARecheck), such an answer means the filesystem
//     reported config.json absent at the moment of the read. See atomicSaveAbsence.
//   - absentRuns counts the maximal runs of consecutive absent loads, and longestAbsentRun is the
//     longest, in loads. The runs, not the loads, are what atomicSaveMaxAbsentRuns bounds.
//   - bad counts everything else: a refusal, any warning or violation, or any other value. These are
//     the reader's failures, and each one fails the test; first is the first of them.
type atomicSaveTally struct {
	reads                        int
	versions                     [2]int
	absent                       int
	absentRuns, longestAbsentRun int
	bad                          int
	first                        string
}

// atomicSaveMaxAbsentRuns is the most runs of absent answers one sub-test may see, a test criterion
// the owner approves (V6 close-out w6-config, review finding 2). It keeps the absent bucket from
// hiding a read failure taken for a missing file.
//
// Derivation. A filesystem absence is one moment during one save, and every load that falls into it
// answers "no file" back to back, so it shows as ONE run however many loads it spans. Measured at
// the fix (plans/sdd/V6-closeout/w6-config/runs/73): of 40 sub-tests, 36 saw no absent load and 4
// saw exactly one run each, of 16, 41, 56 and 92 loads (0.8-4.6% of 2,000 reads). A bound on the
// COUNT of absent loads would have to sit above the longest such moment times the read rate, which
// grows with a slower host, so it is either flaky or too loose to see anything. A read failure
// taken for a missing file is not one moment: it strikes loads spread across the whole run. At the
// base f6095e2, whose config.Load took a failed read for a missing file, config.Load found no file
// 105 to 475 times per sub-test in 105 to 435 separate runs, the longest 5 loads (runs/74); the
// movefileex sub-test passed there before this bound (runs/42) and fails with it. At the measured
// 4 runs in 40 sub-tests (0.1 per sub-test), four or more runs in one sub-test is Poisson-rare
// (about 4e-6), and still rare at five times that rate (about 2e-3 at 0.5 per sub-test). If it is
// too low, a host slower than the one measured fails this test with no defect; if it is too high, a
// read-failure regression that strikes only a few times per run passes.
const atomicSaveMaxAbsentRuns = 3

// atomicSaveAbsence is why an absent answer is not a failure. On the development host (Windows 11,
// NTFS) a rename-replace that SUCCEEDS can leave the destination name missing for tens of
// milliseconds, inside one save that runs slow, with no reader holding the file at all: a monitor
// that only Lstats the name saw ERROR_FILE_NOT_FOUND for 18-115 ms inside 4 of 40,000 MoveFileEx
// saves, 1 of 40,000 paths.WriteAtomic saves and 2 of 60,000 bare POSIX-semantics renames, and a
// paced paths.ReadFileShared reader saw it 38 times in 30,000 reads
// (plans/sdd/V6-closeout/w6-config/runs/5x-diag-*). At that moment the file does not exist, for
// this reader or any other, so no reader can answer anything but "no file". It is the residual
// docs/architecture.md records for the user-global layer's config.json, not something this test can
// hold a reader to.
const atomicSaveAbsence = "the filesystem reported config.json absent during a rename-replace"

// loadUnderAtomicSaves runs load in a loop against a writer saving the project config atomically and
// classifies every answer.
func loadUnderAtomicSaves(t *testing.T, how atomicSave, load func(env config.Env) atomicSaveLoad) (
	atomicSaveTally, *atomicSaveWriter,
) {
	t.Helper()
	env := baseEnv(t)
	absentBudget := config.Defaults().Checkpoint.BudgetTokens
	var tally atomicSaveTally
	w := startAtomicSaveWriter(t, env.ProjectRoot, how)
	run := 0
	for tally.reads < atomicSaveReads || w.attempts.Load() < atomicSaveAttempts {
		tally.reads++
		got := load(env)
		absent := got.err == nil && got.notes == 0 && got.budget == absentBudget
		switch {
		case absent && run == 0:
			tally.absentRuns++
			run = 1
		case absent:
			run++
		default:
			run = 0
		}
		tally.longestAbsentRun = max(tally.longestAbsentRun, run)
		var why string
		switch {
		case got.err != nil:
			why = "refused: " + got.err.Error()
		case got.notes != 0:
			why = fmt.Sprintf("%d warning(s) or violation(s): the file was not read cleanly", got.notes)
		case got.budget == atomicSaveBudgets[0]:
			tally.versions[0]++
		case got.budget == atomicSaveBudgets[1]:
			tally.versions[1]++
		case got.budget == absentBudget:
			tally.absent++
		default:
			why = fmt.Sprintf("budgetTokens %d, which the writer never saved", got.budget)
		}
		if why != "" {
			tally.bad++
			if tally.first == "" {
				tally.first = why
			}
		}
	}
	w.stop()
	return tally, w
}

// TestLoadForCapture_ReadsThroughAnEditorsAtomicSaves is D22's reason for existing. The hook path
// reads .qompack/config.json on every capture, and under D8 a config it cannot read refuses the
// capture outright. On Windows an ordinary os.Open carries no FILE_SHARE_DELETE, so an atomic save
// landing while a hook read the file failed one of the two: the hook's open was refused while the
// rename was finishing, or the rename was refused while the hook's handle was open; and the old
// Lstat/os.SameFile identity check refused whenever a save landed between its two halves. A user
// saving their config could make a hook record nothing. No load may refuse or warn, and every load
// must answer one of the two versions the writer saves, or no file at a moment the filesystem
// reported none (atomicSaveAbsence).
func TestLoadForCapture_ReadsThroughAnEditorsAtomicSaves(t *testing.T) {
	requireLoadsSurviveAtomicSaves(t, func(env config.Env) atomicSaveLoad {
		cfg, _, violations, warnings, err := config.LoadForCapture(env)
		return atomicSaveLoad{budget: cfg.Checkpoint.BudgetTokens, notes: len(violations) + len(warnings), err: err}
	})
}

// TestLoad_ReadsThroughAnEditorsAtomicSaves is the same race through config.Load, the loader the
// daemon's reload, `config print`, doctor and self-test use. It never refuses, so here the defect
// was quieter: a read the save made fail was taken for a missing file, and the whole project layer
// fell back to the defaults without a warning. A failed read of a file that exists is a warning now
// (TestLoad_UnreadableFileWarns), and this test fails on any.
func TestLoad_ReadsThroughAnEditorsAtomicSaves(t *testing.T) {
	requireLoadsSurviveAtomicSaves(t, func(env config.Env) atomicSaveLoad {
		cfg, _, warns, err := config.Load(env)
		return atomicSaveLoad{budget: cfg.Checkpoint.BudgetTokens, notes: len(warns), err: err}
	})
}

func requireLoadsSurviveAtomicSaves(t *testing.T, load func(env config.Env) atomicSaveLoad) {
	t.Helper()
	for _, how := range atomicSaves {
		t.Run(how.name, func(t *testing.T) {
			tally, w := loadUnderAtomicSaves(t, how, load)
			firstErr, _ := w.firstErr.Load().(string)
			t.Logf("%d loads: %d and %d answered the two saved versions, %d found no file in %d run(s), "+
				"the longest %d loads (%s), %d wrong; %d saves attempted, %d refused (first: %s)",
				tally.reads, tally.versions[0], tally.versions[1], tally.absent, tally.absentRuns,
				tally.longestAbsentRun, atomicSaveAbsence, tally.bad, w.attempts.Load(), w.refused.Load(),
				firstErr)
			require.Zero(t, tally.bad, "of %d loads under atomic saves, %d refused, warned or answered a "+
				"version the writer never saved (first: %s)", tally.reads, tally.bad, tally.first)
			require.LessOrEqual(t, tally.absentRuns, atomicSaveMaxAbsentRuns, "of %d loads, %d found no "+
				"file in %d separate runs: more than one rename-replace's absent moment explains, so a "+
				"read failure is being taken for a missing file", tally.reads, tally.absent, tally.absentRuns)
			require.Positive(t, tally.versions[0], "no load read the first saved version: the loads are "+
				"not reading the file the writer saves")
			require.Positive(t, tally.versions[1], "no load read the second saved version: the loads are "+
				"not reading the file the writer saves")
			if how.savesMustLand {
				require.Zero(t, w.refused.Load(), "of %d atomic saves, %d were refused while a load held "+
					"the file (first: %s)", w.attempts.Load(), w.refused.Load(), firstErr)
			}
		})
	}
}
