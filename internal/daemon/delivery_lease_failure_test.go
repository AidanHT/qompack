package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

type leaseFaultWriter struct {
	file  *os.File
	write func([]byte) (int, error)
	sync  func() error
	close func() error
}

func (w leaseFaultWriter) Write(b []byte) (int, error) {
	if w.write != nil {
		return w.write(b)
	}
	return w.file.Write(b)
}

func (w leaseFaultWriter) Sync() error {
	if w.sync != nil {
		return w.sync()
	}
	return w.file.Sync()
}

func (w leaseFaultWriter) Close() error {
	if w.close != nil {
		return w.close()
	}
	return w.file.Close()
}

func TestDeliveryJournal_LeaseSyncSerializesWithRelease(t *testing.T) {
	_, lock, journal := newTestDeliveryJournal(t)
	entered, proceed := make(chan struct{}), make(chan struct{})
	var releaseSync sync.Once
	unblock := func() { releaseSync.Do(func() { close(proceed) }) }
	t.Cleanup(unblock)
	journal.writer = leaseFaultWriter{file: journal.file, sync: func() error {
		close(entered)
		<-proceed
		return journal.file.Sync()
	}}
	done := make(chan error, 1)
	go func() {
		_, err := journal.lease(context.Background(), testDeliveryToken('a'), "session", testDeliveryRequest("x"))
		done <- err
	}()
	<-entered
	position, err := journal.loadPosition()
	require.NoError(t, err)
	require.Zero(t, position.Count, "the position cannot advance ahead of object-independent journal Sync")
	select {
	case err := <-done:
		t.Fatalf("lease returned before Sync: %v", err)
	default:
	}
	released := make(chan error, 1)
	go func() { released <- lock.Release() }()
	select {
	case err := <-released:
		t.Fatalf("Release overtook the lease: %v", err)
	default:
	}
	unblock()
	require.NoError(t, <-done)
	require.NoError(t, <-released)
	position, err = journal.loadPosition()
	require.NoError(t, err)
	require.Equal(t, 1, position.Count)
	_, err = journal.lease(context.Background(), testDeliveryToken('b'), "session", testDeliveryRequest("x"))
	require.Error(t, err)
}

func TestDeliveryJournal_UncertainAppendPoisonsUntilRecovery(t *testing.T) {
	for _, mode := range []string{"short write", "sync failure", "position failure"} {
		t.Run(mode, func(t *testing.T) {
			root, lock, journal := newTestDeliveryJournal(t)
			positionPath := filepath.Join(paths.Of(root).State, deliveryPositionFile)
			position, err := os.ReadFile(positionPath)
			require.NoError(t, err)
			switch mode {
			case "short write":
				journal.writer = leaseFaultWriter{file: journal.file, write: func(b []byte) (int, error) { return journal.file.Write(b[:len(b)/2]) }}
			case "sync failure":
				journal.writer = leaseFaultWriter{file: journal.file, sync: func() error { return errors.New("private backend fixture") }}
			case "position failure":
				journal.writer = leaseFaultWriter{file: journal.file, sync: func() error {
					err := journal.file.Sync()
					require.NoError(t, os.Remove(positionPath))
					require.NoError(t, os.Mkdir(positionPath, 0o700))
					return err
				}}
			}
			lease, err := journal.lease(context.Background(), testDeliveryToken('a'), "session", testDeliveryRequest("x"))
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private backend fixture")
			require.Equal(t, deliveryLease{}, lease)
			require.Empty(t, journal.leases)
			require.Zero(t, journal.arrivals["session"])
			before, err := os.ReadFile(journal.path)
			require.NoError(t, err)
			_, err = journal.lease(context.Background(), testDeliveryToken('b'), "session", testDeliveryRequest("x"))
			require.Error(t, err)
			after, err := os.ReadFile(journal.path)
			require.NoError(t, err)
			require.Equal(t, before, after)
			require.NoError(t, lock.Release())
			if mode == "position failure" {
				require.NoError(t, os.Remove(positionPath)) // empty, exclusively owned fixture directory
				require.NoError(t, os.WriteFile(positionPath, position, 0o600))
			}
			next, err := acquireTestDeliveryLock(root)
			require.NoError(t, err)
			t.Cleanup(func() { _ = next.Release() })
			recovered, err := next.openDeliveryJournal()
			if mode == "short write" {
				require.Error(t, err)
				require.Nil(t, recovered)
				after, err = os.ReadFile(journal.path)
				require.NoError(t, err)
				require.Equal(t, before, after, "a torn tail is preserved, never consumed or truncated")
				return
			}
			require.NoError(t, err)
			lease, err = recovered.lease(context.Background(), testDeliveryToken('a'), "session", testDeliveryRequest("x"))
			require.NoError(t, err)
			require.Equal(t, uint64(1), lease.ArrivalSeq)
			sealed, err := recovered.loadPosition()
			require.NoError(t, err)
			require.Equal(t, 1, sealed.Count, "a surviving complete tail must be synced and sealed before retry")
		})
	}
}

func TestDeliveryJournal_CloseFailureRetainsOwnership(t *testing.T) {
	root, lock, journal := newTestDeliveryJournal(t)
	journal.writer = leaseFaultWriter{file: journal.file, close: func() error { return errors.New("private close fixture") }}
	require.Error(t, lock.Release())
	_, err := os.Stat(LockPath(root))
	require.NoError(t, err)
	_, err = acquireTestDeliveryLock(root)
	require.ErrorIs(t, err, ErrLockHeld)
	_, err = journal.lease(context.Background(), testDeliveryToken('a'), "session", testDeliveryRequest("x"))
	require.Error(t, err)
	journal.writer = journal.file // repair only the owned failure-injection seam before cleanup
	require.NoError(t, lock.Release())
}

func TestDeliveryJournal_SealedPrefixCannotRewindOrChange(t *testing.T) {
	for _, mode := range []string{"truncate", "mutate", "missing journal", "missing position"} {
		t.Run(mode, func(t *testing.T) {
			root, lock, journal := newTestDeliveryJournal(t)
			_, err := journal.lease(context.Background(), testDeliveryToken('a'), "session", testDeliveryRequest("x"))
			require.NoError(t, err)
			_, err = journal.lease(context.Background(), testDeliveryToken('b'), "session", testDeliveryRequest("x"))
			require.NoError(t, err)
			require.NoError(t, lock.Release())
			positionPath := filepath.Join(paths.Of(root).State, deliveryPositionFile)
			switch mode {
			case "truncate":
				line, err := os.ReadFile(journal.path)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(journal.path, line[:bytes.IndexByte(line, '\n')+1], 0o600))
			case "mutate":
				line, err := os.ReadFile(journal.path)
				require.NoError(t, err)
				var row deliveryLease
				end := bytes.IndexByte(line, '\n') + 1
				require.NoError(t, json.Unmarshal(line[:end], &row))
				row.RequestHash = testDeliveryRequest("changed")
				changed, err := json.Marshal(row)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(journal.path, append(append(changed, '\n'), line[end:]...), 0o600))
			case "missing journal":
				require.NoError(t, os.Remove(journal.path))
			case "missing position":
				require.NoError(t, os.Remove(positionPath))
			}
			before, _ := os.ReadFile(journal.path)
			next, err := acquireTestDeliveryLock(root)
			require.NoError(t, err)
			t.Cleanup(func() { _ = next.Release() })
			got, err := next.openDeliveryJournal()
			require.Error(t, err)
			require.Nil(t, got)
			after, _ := os.ReadFile(journal.path)
			require.Equal(t, before, after, "failed recovery must preserve its evidence")
		})
	}
}

func TestDeliveryJournal_BoundsAndSequenceOverflowRefuseAdmission(t *testing.T) {
	_, _, journal := newTestDeliveryJournal(t)
	_, err := journal.lease(context.Background(), testDeliveryToken('a'), core.SessionID(strings.Repeat("s", deliveryLeaseMaxLine)), testDeliveryRequest("x"))
	require.ErrorIs(t, err, core.ErrBudget)
	journal.arrivals["exhausted"] = math.MaxUint64
	_, err = journal.lease(context.Background(), testDeliveryToken('a'), "exhausted", testDeliveryRequest("x"))
	require.ErrorIs(t, err, core.ErrBudget)
	_, err = journal.lease(context.Background(), testDeliveryToken('a'), core.SessionID([]byte{0xff}), testDeliveryRequest("x"))
	require.ErrorIs(t, err, core.ErrContract)
	contents, err := os.ReadFile(journal.path)
	require.NoError(t, err)
	require.Empty(t, contents)
}

func TestDeliveryJournal_ConcurrentDeliveryRetriesShareOneAssignment(t *testing.T) {
	_, _, journal := newTestDeliveryJournal(t)
	const workers = 8
	results := make(chan deliveryLease, workers)
	errors := make(chan error, workers)
	for range workers {
		go func() {
			lease, err := journal.lease(context.Background(), testDeliveryToken('a'), "session", testDeliveryRequest("x"))
			results <- lease
			errors <- err
		}()
	}
	var first deliveryLease
	for range workers {
		require.NoError(t, <-errors)
		lease := <-results
		if first.ArrivalSeq == 0 {
			first = lease
		}
		require.Equal(t, first, lease)
	}
	contents, err := os.ReadFile(journal.path)
	require.NoError(t, err)
	require.Equal(t, 1, bytes.Count(contents, []byte{'\n'}))
}

// TestDeliveryJournal_PositionCorruptionCannotBeRepairedByAnOpenWriter is one of design §6.1's
// step-2 trace rows, and it names no format: it runs against whatever seal the build holds. The
// outcomes it asserts are the same in both, but the leg that discriminates between its modes is not:
//
//   - At format 1 the open writer's per-batch check reads the sidecar back as a v1 document, so each
//     non-missing mode is refused by its own predicate — malformed by the parse, future by the
//     version, noncanonical by the canonical re-encode, and count, bytes and chain by the comparison
//     with the journal's own position.
//   - At format 2 all six of those modes write the same ~110-byte document over a held 32 KiB image,
//     so the seal's identity check refuses every one of them on SIZE, before anything is read. The
//     per-mode discrimination then lives on the REOPEN leg below, where the file is no longer a v2
//     image and the v1 reader takes it. The v2-shaped forms of these corruptions, each reaching its
//     own predicate against a held seal, are TestDeliverySeal_StepTwoTraceHoldsInFormatTwo's and
//     t14Corruptions' format-2 modes.
//
// What the test itself pins is format-free: an open writer never repairs a corrupt position, the
// journal file is left byte for byte as it was, the corruption is preserved rather than overwritten,
// and the next owner cannot open the journal either.
func TestDeliveryJournal_PositionCorruptionCannotBeRepairedByAnOpenWriter(t *testing.T) {
	for _, mode := range []string{"missing", "malformed", "future", "noncanonical", "count", "bytes", "chain"} {
		t.Run(mode, func(t *testing.T) {
			root, lock, journal := newTestDeliveryJournal(t)
			_, err := journal.lease(context.Background(), testDeliveryToken('a'), "session", testDeliveryRequest("x"))
			require.NoError(t, err)
			positionPath := filepath.Join(paths.Of(root).State, deliveryPositionFile)
			position, err := journal.loadPosition()
			require.NoError(t, err)
			var changed []byte
			switch mode {
			case "missing":
				require.NoError(t, os.Remove(positionPath))
			case "malformed":
				changed = []byte(`{"private fixture":`)
			case "future":
				position.Version++
			case "count":
				position.Count++
			case "bytes":
				position.Bytes--
			case "chain":
				position.Chain = testDeliveryRequest("unrelated chain")
			}
			if mode != "missing" {
				if changed == nil {
					changed, err = json.Marshal(position)
					require.NoError(t, err)
				}
				if mode == "noncanonical" {
					changed = append(changed, ' ')
				}
				require.NoError(t, os.WriteFile(positionPath, changed, 0o600))
			}
			before, err := os.ReadFile(journal.path)
			require.NoError(t, err)
			_, err = journal.lease(context.Background(), testDeliveryToken('b'), "session", testDeliveryRequest("x"))
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private fixture")
			after, err := os.ReadFile(journal.path)
			require.NoError(t, err)
			require.Equal(t, before, after)
			require.NoError(t, lock.Release())
			next, err := acquireTestDeliveryLock(root)
			require.NoError(t, err)
			t.Cleanup(func() { _ = next.Release() })
			got, err := next.openDeliveryJournal()
			require.Error(t, err)
			require.Nil(t, got)
			preserved, readErr := os.ReadFile(positionPath)
			if mode == "missing" {
				require.True(t, os.IsNotExist(readErr))
			} else {
				require.NoError(t, readErr)
				require.Equal(t, changed, preserved)
			}
		})
	}
}

func TestDeliveryJournal_FailedOpenRequiresOwnerReleaseBeforeRetry(t *testing.T) {
	root, lock, journal := newTestDeliveryJournal(t)
	require.NoError(t, lock.Release())
	positionPath := filepath.Join(paths.Of(root).State, deliveryPositionFile)
	position, err := os.ReadFile(positionPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(positionPath, []byte(`{`), 0o600))
	next, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = next.Release() })
	_, err = next.openDeliveryJournal()
	require.Error(t, err)
	require.NoError(t, os.WriteFile(positionPath, position, 0o600))
	_, err = next.openDeliveryJournal()
	require.Error(t, err, "repair does not silently clear an uncertain owner's state")
	require.NoError(t, next.Release())
	last, err := acquireTestDeliveryLock(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = last.Release() })
	recovered, err := last.openDeliveryJournal()
	require.NoError(t, err)
	require.Equal(t, journal.bytes, recovered.bytes)
}

func TestDeliveryJournal_RejectsOversizeJournalAndRecord(t *testing.T) {
	for _, physicalFile := range []bool{false, true} {
		t.Run(map[bool]string{false: "record", true: "file"}[physicalFile], func(t *testing.T) {
			root, lock, journal := newTestDeliveryJournal(t)
			require.NoError(t, lock.Release())
			if physicalFile {
				file, err := os.OpenFile(journal.path, os.O_WRONLY, 0o600)
				require.NoError(t, err)
				require.NoError(t, file.Truncate(deliveryLeaseMaxBytes+1))
				require.NoError(t, file.Close())
			} else {
				require.NoError(t, os.WriteFile(journal.path, bytes.Repeat([]byte{'x'}, deliveryLeaseMaxLine+1), 0o600))
			}
			before, err := os.Stat(journal.path)
			require.NoError(t, err)
			next, err := acquireTestDeliveryLock(root)
			require.NoError(t, err)
			t.Cleanup(func() { _ = next.Release() })
			got, err := next.openDeliveryJournal()
			require.Error(t, err)
			require.Nil(t, got)
			after, err := os.Stat(journal.path)
			require.NoError(t, err)
			require.Equal(t, before.Size(), after.Size())
		})
	}
}
