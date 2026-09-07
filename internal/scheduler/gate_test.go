package scheduler

import (
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// pSelectionAtProcessStart is PSelectionAvailable() as observed before any test ran. Recording it
// in TestMain is what lets TestPSelectionAvailable_DefaultsFalse assert the PROCESS default rather
// than whatever the previous test left behind.
var pSelectionAtProcessStart bool

// TestMain is the package's single TestMain.
func TestMain(m *testing.M) {
	pSelectionAtProcessStart = PSelectionAvailable()
	os.Exit(m.Run())
}

// TestPSelectionAvailable_DefaultsFalse guards the closing-note priority-3 ship order: the flag
// is false until a real scheduler Runtime enables it, so analyzer.NewSelector refuses to
// construct in every process that has not built one.
func TestPSelectionAvailable_DefaultsFalse(t *testing.T) {
	require.False(t, pSelectionAtProcessStart, "p-selection must be off at process start")
}

func TestEnableDisablePSelection(t *testing.T) {
	defer DisablePSelection()

	EnablePSelection()
	require.True(t, PSelectionAvailable())
	EnablePSelection()
	require.True(t, PSelectionAvailable(), "enable is idempotent")

	DisablePSelection()
	require.False(t, PSelectionAvailable())
	DisablePSelection()
	require.False(t, PSelectionAvailable(), "disable is idempotent")

	EnablePSelection()
	require.True(t, PSelectionAvailable(), "the flag can be raised again after shutdown")
}

// TestPSelectionGate_ConcurrentAccess runs 64 readers against one writer. On the shipped plain
// bool this is a data race under -race; on atomic.Bool it is clean.
func TestPSelectionGate_ConcurrentAccess(t *testing.T) {
	defer DisablePSelection()

	const (
		readers = 64
		toggles = 2_000
	)
	var (
		wg    sync.WaitGroup
		reads atomic.Int64
		stop  = make(chan struct{})
	)
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				// Read first so every reader contributes at least once even when the writer
				// finishes every toggle before this goroutine is first scheduled.
				_ = PSelectionAvailable()
				reads.Add(1)
				select {
				case <-stop:
					return
				default:
				}
				runtime.Gosched()
			}
		}()
	}
	for i := 0; i < toggles; i++ {
		if i%2 == 0 {
			EnablePSelection()
		} else {
			DisablePSelection()
		}
	}
	close(stop)
	wg.Wait()

	require.False(t, PSelectionAvailable(), "the last toggle was a disable")
	require.Positive(t, reads.Load())
}
