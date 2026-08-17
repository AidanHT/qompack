package ipc_test

import (
	"os"
	"path/filepath"
	"sync"
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
	require.EqualValues(t, 5, got.ConnectDeadlineMs)
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
