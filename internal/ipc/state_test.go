package ipc_test

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
)

// TestStateRoundTrip is task-1-spec's table row: every field of a fully populated State survives a
// WriteState/ReadState round trip, and the file is exactly 32 bytes.
func TestStateRoundTrip(t *testing.T) {
	root := t.TempDir()
	want := ipc.State{
		Mode:              contract.ModeDegradedPassive,
		Hot:               ipc.HotSpool,
		ConnectDeadlineMs: 5,
		AckDeadlineMs:     8,
		DaemonEnabled:     true,
		SpoolOnBreach:     true,
		MaxPayloadBytes:   1048576,
		DaemonPID:         4242,
		Written:           core.UnixMilli(1730000000000),
	}

	require.NoError(t, ipc.WriteState(root, want))

	got := ipc.ReadState(root, config.Defaults())
	require.Equal(t, want, got)

	fi, err := os.Stat(ipc.StatePath(root))
	require.NoError(t, err)
	require.EqualValues(t, 32, fi.Size())
}

// TestStateMissingFallsBackToDefaults asserts the normal first-run case: no state file at all
// falls back to StateFromConfig(fallback), silently.
func TestStateMissingFallsBackToDefaults(t *testing.T) {
	root := t.TempDir()
	got := ipc.ReadState(root, config.Defaults())
	require.Equal(t, contract.ModeFull, got.Mode)
	require.Equal(t, ipc.HotSync, got.Hot)
	require.EqualValues(t, 8, got.AckDeadlineMs)
	// connectDeadlineMs's default is platform-specific (internal/config/deadlines.go: on Windows
	// it has to clear the named-pipe dial's busy-retry quantum), so what this row asserts is that
	// the fallback carried the CONFIG's value through, not a second spelling of the number.
	// config's own TestDefaults_RuntimeNamespace is where the value itself is pinned.
	require.EqualValues(t, config.Defaults().Runtime.Daemon.ConnectDeadlineMs, got.ConnectDeadlineMs)
}

// TestStateBadCRCFallsBack asserts a state file that has been corrupted in place (bytes correct
// length and magic, but the payload no longer matches its own CRC) falls back exactly like a
// missing file, rather than surfacing garbage.
func TestStateBadCRCFallsBack(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, ipc.WriteState(root, ipc.State{
		Mode: contract.ModeDegradedPassive, Hot: ipc.HotSpool,
		ConnectDeadlineMs: 5, AckDeadlineMs: 8, DaemonEnabled: true,
		MaxPayloadBytes: 1048576, DaemonPID: 1, Written: 1,
	}))

	b, err := os.ReadFile(ipc.StatePath(root))
	require.NoError(t, err)
	b[12] ^= 0xFF // inside maxPayloadBytes, before the CRC field
	require.NoError(t, os.WriteFile(ipc.StatePath(root), b, 0o600))

	fallback := config.Defaults()
	require.Equal(t, ipc.StateFromConfig(fallback), ipc.ReadState(root, fallback))
}

// TestStateShortFileFallsBack asserts a truncated (17-byte) state file falls back rather than
// panicking on an out-of-range slice access.
func TestStateShortFileFallsBack(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Dir(ipc.StatePath(root)), 0o700))
	require.NoError(t, os.WriteFile(ipc.StatePath(root), make([]byte, 17), 0o600))

	fallback := config.Defaults()
	require.Equal(t, ipc.StateFromConfig(fallback), ipc.ReadState(root, fallback))
}

// TestStateBadMagicFallsBack asserts a 32-byte file that simply isn't a state record (wrong magic)
// falls back rather than being decoded as garbage.
func TestStateBadMagicFallsBack(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Dir(ipc.StatePath(root)), 0o700))
	require.NoError(t, os.WriteFile(ipc.StatePath(root), make([]byte, 32), 0o600)) // all zero bytes

	fallback := config.Defaults()
	require.Equal(t, ipc.StateFromConfig(fallback), ipc.ReadState(root, fallback))
}

// TestStateWriteIsAtomic runs 500 WriteState calls from the single writer that ever exists in
// practice (the daemon itself never has two goroutines writing state.bin at once), each one
// overlapped with a burst of one-shot readers — modelling reality exactly: every hook invocation
// calls ReadState exactly once and exits, it never polls in a tight loop — under -race. Every read
// must land on a fully valid record: decodeState's own CRC check rules out anything torn, so
// DaemonPID must always be a value the writer actually wrote, never a mix of two writes' bytes.
func TestStateWriteIsAtomic(t *testing.T) {
	root := t.TempDir()
	const (
		writes          = 500
		readersPerWrite = 2
	)
	fallback := config.Defaults()
	valid := map[uint32]bool{0: true} // 0: StateFromConfig's DaemonPID, possible before the first write lands
	for i := uint32(1); i <= writes; i++ {
		valid[i] = true
	}

	var readerWG sync.WaitGroup
	badReads := make(chan uint32, writes*readersPerWrite)
	for i := uint32(1); i <= writes; i++ {
		for r := 0; r < readersPerWrite; r++ {
			readerWG.Add(1)
			go func() {
				defer readerWG.Done()
				if s := ipc.ReadState(root, fallback); !valid[s.DaemonPID] {
					badReads <- s.DaemonPID
				}
			}()
		}

		require.NoError(t, ipc.WriteState(root, ipc.State{
			Mode: contract.ModeFull, Hot: ipc.HotSync,
			ConnectDeadlineMs: 5, AckDeadlineMs: 8, DaemonEnabled: true,
			MaxPayloadBytes: 1048576, DaemonPID: i, Written: core.UnixMilli(i),
		}))
	}
	readerWG.Wait()
	close(badReads)

	for pid := range badReads {
		t.Errorf("read DaemonPID %d, which the writer never produced — a torn or corrupted record", pid)
	}

	final := ipc.ReadState(root, fallback)
	require.True(t, valid[final.DaemonPID])
}

// stateDegradedGolden is the 32-byte fixture this commit creates: mode=1 (degraded-passive),
// hot=1 (spool).
const stateDegradedGolden = "../../testdata/golden/contracts/ipc/state_degraded.bin"

// TestState_DegradedGoldenFixtureDecodes asserts the committed fixture is a well-formed record
// that ReadState decodes to the mode/hot values its name promises, rather than silently falling
// back — the fixture is worthless if it does not actually exercise the degraded/spool path.
func TestState_DegradedGoldenFixtureDecodes(t *testing.T) {
	b, err := os.ReadFile(stateDegradedGolden)
	require.NoError(t, err, "golden fixture missing: %s", stateDegradedGolden)
	require.Len(t, b, 32)

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Dir(ipc.StatePath(root)), 0o700))
	require.NoError(t, os.WriteFile(ipc.StatePath(root), b, 0o600))

	got := ipc.ReadState(root, config.Defaults())
	require.Equal(t, contract.ModeDegradedPassive, got.Mode)
	require.Equal(t, ipc.HotSpool, got.Hot)
}

// TestStatePath asserts the exact §2.4-adjacent location: <root>/.qompack/run/state.bin.
func TestStatePath(t *testing.T) {
	root := filepath.FromSlash("/proj")
	require.Equal(t, filepath.Join(root, ".qompack", "run", "state.bin"), ipc.StatePath(root))
}

// TestRemoveState_IsIdempotent asserts removing a state file that does not exist is not an error —
// the daemon calls RemoveState on clean stop regardless of whether it ever wrote one.
func TestRemoveState_IsIdempotent(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, ipc.RemoveState(root))

	require.NoError(t, ipc.WriteState(root, ipc.State{Mode: contract.ModeFull}))
	require.NoError(t, ipc.RemoveState(root))
	_, err := os.Stat(ipc.StatePath(root))
	require.True(t, os.IsNotExist(err))

	require.NoError(t, ipc.RemoveState(root))
}

// TestStateFromConfig_MapsRuntimeModeVocabulary pins the runtime.mode -> contract.Mode mapping:
// "off" -> ModeOff, "passive" -> ModeDegradedPassive, anything else ("auto", "full", or an
// unrecognized value) -> ModeFull.
func TestStateFromConfig_MapsRuntimeModeVocabulary(t *testing.T) {
	cases := map[string]contract.Mode{
		"off":     contract.ModeOff,
		"passive": contract.ModeDegradedPassive,
		"full":    contract.ModeFull,
		"auto":    contract.ModeFull,
		"":        contract.ModeFull,
		"bogus":   contract.ModeFull,
	}
	for runtimeMode, want := range cases {
		cfg := config.Defaults()
		cfg.Runtime.Mode = runtimeMode
		got := ipc.StateFromConfig(cfg)
		require.Equal(t, want, got.Mode, "runtime.mode=%q", runtimeMode)
	}
}

// TestStateFromConfig_CarriesTheDaemonAndHotPathKnobs asserts every non-Mode field of State comes
// straight from the corresponding runtime.* config leaf, using the shipped field spellings.
func TestStateFromConfig_CarriesTheDaemonAndHotPathKnobs(t *testing.T) {
	cfg := config.Defaults()
	got := ipc.StateFromConfig(cfg)

	require.Equal(t, ipc.HotSync, got.Hot, "a fresh config always starts in the sync submode")
	require.EqualValues(t, cfg.Runtime.Daemon.ConnectDeadlineMs, got.ConnectDeadlineMs)
	require.EqualValues(t, cfg.Runtime.Daemon.AckDeadlineMs, got.AckDeadlineMs)
	require.Equal(t, cfg.Runtime.Daemon.Enabled, got.DaemonEnabled)
	require.Equal(t, cfg.Runtime.HotPath.SpoolOnBreach, got.SpoolOnBreach)
	require.EqualValues(t, cfg.Runtime.HotPath.MaxPayloadBytes, got.MaxPayloadBytes)
}

// stateContentionWrites and stateContentionReaders shape
// TestStateReadNeverFallsBackWhileAValidRecordIsOnDisk's load. They are not a duration and not a
// timeout: the test ends when the writer has finished its writes, whatever that costs on the host.
// The numbers are chosen so a collision is not a rare event that a lucky run can miss — on the
// Windows host this was written against, 50 writes against 4 spinning readers produce tens of
// thousands of reads, of which thousands hit the rename window before the fix.
const (
	stateContentionWrites  = 50
	stateContentionReaders = 4
)

// TestStateReadNeverFallsBackWhileAValidRecordIsOnDisk pins the half of ReadState's contract that
// its own doc comment states and that TestStateWriteIsAtomic structurally cannot see: the fallback
// exists for a record that is MISSING, short, bad-magic or bad-CRC — four cases, the same four
// SP-05 §"state.bin" lists — and a transient refusal by the OS to open a file that is present and
// valid is none of them.
//
// The failure it guards against is Windows-only and was measured, not theorised. paths.WriteAtomic
// finishes with os.Rename onto the destination; for the instant of that replace a concurrent
// os.Open of state.bin fails with ERROR_SHARING_VIOLATION ("The process cannot access the file
// because it is being used by another process"). Swallowed, that error becomes
// StateFromConfig(fallback): Hot HotSync, Mode ModeFull, DaemonPID 0. A hook client that reads
// state.bin in that instant therefore concludes the daemon is healthy and in sync submode, and
// dials it — which is precisely the connect §12.2's spool submode exists to stop it making — and
// it does so using default deadlines rather than the operator's.
//
// TestStateWriteIsAtomic cannot catch this because it admits DaemonPID 0 as a valid answer
// ("possible before the first write lands"), so every fail-open read there counts as a pass. This
// test writes a full record before any reader starts, so no field's fallback value is ever a
// legitimate answer afterwards.
//
// On POSIX there is no such contention and this test passes with or without the fix; it is a
// Windows guard that costs a second elsewhere.
func TestStateReadNeverFallsBackWhileAValidRecordIsOnDisk(t *testing.T) {
	root := t.TempDir()
	fallback := config.Defaults()

	// Every field below differs from what StateFromConfig(config.Defaults()) would produce, so a
	// fallback read is detectable on any of them rather than only on the one under investigation.
	rec := func(pid uint32) ipc.State {
		return ipc.State{
			Mode: contract.ModeDegradedPassive, Hot: ipc.HotSpool,
			ConnectDeadlineMs: 5, AckDeadlineMs: 8, DaemonEnabled: true, SpoolOnBreach: true,
			MaxPayloadBytes: 1048576, DaemonPID: pid, Written: core.UnixMilli(pid),
		}
	}
	require.NoError(t, ipc.WriteState(root, rec(1)),
		"the record every reader below must see has to be on disk before any of them starts")

	var reads, fellBackHot, fellBackMode, fellBackPID atomic.Int64
	stop := make(chan struct{})
	var stopOnce sync.Once
	closeStop := func() { stopOnce.Do(func() { close(stop) }) }
	t.Cleanup(closeStop) // releases the readers even if the writer loop below fails the test

	var wg sync.WaitGroup
	for r := 0; r < stateContentionReaders; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				got := ipc.ReadState(root, fallback)
				reads.Add(1)
				if got.Hot != ipc.HotSpool {
					fellBackHot.Add(1)
				}
				if got.Mode != contract.ModeDegradedPassive {
					fellBackMode.Add(1)
				}
				if got.DaemonPID == 0 {
					fellBackPID.Add(1)
				}
			}
		}()
	}

	for i := uint32(2); i <= stateContentionWrites+1; i++ {
		require.NoError(t, ipc.WriteState(root, rec(i)))
	}
	closeStop()
	wg.Wait()

	require.Positive(t, reads.Load(), "no reader ever ran, so this test proved nothing")
	const why = "%d of %d ReadState calls returned StateFromConfig's %s while a valid record was " +
		"on disk the whole time: a transient open failure is not one of ReadState's four " +
		"documented fallbacks, and swallowing it makes a hook client dial a daemon that has " +
		"already degraded (§12.2)"
	require.Zero(t, fellBackHot.Load(), why, fellBackHot.Load(), reads.Load(), "Hot=sync")
	require.Zero(t, fellBackMode.Load(), why, fellBackMode.Load(), reads.Load(), "Mode=full")
	require.Zero(t, fellBackPID.Load(), why, fellBackPID.Load(), reads.Load(), "DaemonPID=0")
}
